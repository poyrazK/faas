// Snapshot backing identity — ADR-510.
//
// A Firecracker snapshot's guest RAM holds the guest kernel's page cache and
// ext4 metadata for every read-only drive the VM booted with. Restoring that
// RAM onto a different image — even one with byte-identical files at other
// block offsets — makes the guest read the wrong blocks for every page it
// had not cached. The shared runtime base is resolved through a logical key
// that a release refresh replaces in place (Manager.ensureBaseGeneration),
// so a snapshot captured before a refresh used to be restored onto the new
// base. On production-us that crashed a Node function after an init restore
// (# Check failed: !is_iterable(), 2026-10-02); a synthetic rebuild of the
// same base kills every restored interpreter.
//
// The Manager therefore records the content identity of the kernel and base
// a VM booted with, writes it next to each capture, and refuses a restore
// whose recorded identity is missing or differs. A refused restore never
// starts the VM; it cold-boots, and schedd marks the snapshot stale so the
// next park captures against the current images.

package fcvm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// backingIdentityVersion versions the sidecar document. Version 2 adds the
// guest timer profile (ADR-642): every version-1 capture was booted with the
// tsc/lapic-deadline timers that lose interrupts after a restore, so it is
// refused once and re-captured from a cold boot.
const backingIdentityVersion = 2

var (
	// ErrSnapshotBackingUnverified: the capture has no backing identity
	// (taken before ADR-510, or its identity was never recorded), or the
	// current images cannot be identified. It cannot be restored safely.
	ErrSnapshotBackingUnverified = errors.New("fcvm: snapshot backing images unverified")
	// ErrSnapshotBackingChanged: the kernel or base differs from the images
	// the snapshot was captured with.
	ErrSnapshotBackingChanged = errors.New("fcvm: snapshot backing images changed")
)

// BackingIdentity is the content identity of a VM's read-only backing
// images. The private writable drive is bound to each capture separately
// (state.SnapshotDriveKey) and is not part of it.
type BackingIdentity struct {
	Version int    `json:"version"`
	Kernel  string `json:"kernel"`
	Base    string `json:"base"`
	// Timer is the guest timekeeping kernel command line the VM booted
	// with. A restore keeps the boot's clocksource and clockevent, so it
	// is part of what a capture can be restored onto.
	Timer string `json:"timer"`
}

func (b BackingIdentity) complete() bool {
	return b.Version == backingIdentityVersion && b.Kernel != "" && b.Base != "" && b.Timer != ""
}

// fileIdentityKey identifies one immutable file version. Cache refreshes
// and re-stages always write a temp file and rename it, so a replaced image
// gets a new inode; mtime is deliberately excluded because the cache's LRU
// touch rewrites it on every read. The birth time, where the filesystem
// records one, keeps a recycled inode number of the same size (bases are
// sized in whole megabytes, so equal sizes are common) from inheriting the
// digest of the evicted file it replaced.
type fileIdentityKey struct {
	dev, ino  uint64
	size      int64
	btimeSec  int64
	btimeNsec uint32
}

// backingDigestRecorder keeps a file version's digest with the file itself,
// so a vmmd restart reuses it instead of hashing every base again on the wake
// path (ADR-510 amendment 2). recorded must only return a digest that was
// recorded for this very file version and size.
type backingDigestRecorder interface {
	recorded(f *os.File, size int64) (string, bool)
	record(f *os.File, size int64, digest string) error
}

// recordedBackingDigestVersion versions the recorded value.
const recordedBackingDigestVersion = "v1"

func formatRecordedBackingDigest(size int64, digest string) string {
	return fmt.Sprintf("%s %d %s", recordedBackingDigestVersion, size, digest)
}

// parseRecordedBackingDigest accepts only a well-formed record for size.
func parseRecordedBackingDigest(value string, size int64) (string, bool) {
	fields := strings.Fields(value)
	if len(fields) != 3 || fields[0] != recordedBackingDigestVersion || fields[1] != strconv.FormatInt(size, 10) {
		return "", false
	}
	digest := fields[2]
	sum, ok := strings.CutPrefix(digest, "sha256:")
	if !ok || len(sum) != sha256.Size*2 {
		return "", false
	}
	if _, err := hex.DecodeString(sum); err != nil || strings.ToLower(sum) != sum {
		return "", false
	}
	return digest, true
}

func (m *Manager) digestRecorder() backingDigestRecorder {
	if m.backingRecorder != nil {
		return m.backingRecorder
	}
	return defaultBackingDigestRecorder()
}

// backingEnabled reports whether this Manager enforces ADR-510. Production
// vmmd always wires a storage backend; managers without one (unit tests that
// fake the VMM) keep the pre-ADR-510 behaviour.
func (m *Manager) backingEnabled() bool { return m.storage != nil }

// resolveBackingFile maps a kernel/base key to the local file the VMM
// attaches: absolute keys are host paths; logical keys resolve through the
// storage backend's local cache.
func (m *Manager) resolveBackingFile(key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("%w: empty image key", ErrSnapshotBackingUnverified)
	}
	if filepath.IsAbs(key) {
		return key, nil
	}
	resolver, ok := m.storage.(storage.LocalPathResolver)
	if !ok {
		return "", fmt.Errorf("%w: storage cannot resolve %q to a local file", ErrSnapshotBackingUnverified, key)
	}
	path, local, err := resolver.LocalPath(key)
	if err != nil {
		return "", fmt.Errorf("%w: resolve %q: %w", ErrSnapshotBackingUnverified, key, err)
	}
	if !local {
		return "", fmt.Errorf("%w: %q %w", ErrSnapshotBackingUnverified, key, errBackingNotCached)
	}
	return path, nil
}

// errBackingNotCached marks a logical image key that resolves to no file in
// this node's cache.
var errBackingNotCached = errors.New("is not cached locally")

// errBackingCheckFailed marks a backing refusal caused by this node rather
// than the capture: the identity record could not be read (other than being
// absent), or this node's kernel or base could not be fetched or hashed.
var errBackingCheckFailed = errors.New("backing check failed on this node")

// fetchBackingFile is resolveBackingFile for a restore that is about to
// attach the image: a logical key missing from this node's cache is read
// through the storage backend first, which installs it in the cache, and then
// identified as usual. Without the fetch the restore refused and cold-booted,
// and the cold boot fetched the very same base anyway: on production-us
// (rc.288, 2026-10-21) a wake whose base had been evicted from fsn-2's cache
// took 6.7 s (base 0.8 s, app layer 3.5 s, boot 1.3 s) where fetching the
// base and restoring takes about 1 s. A fetched image that differs from the
// capture's is still refused by the identity comparison.
func (m *Manager) fetchBackingFile(ctx context.Context, key string) (string, error) {
	path, err := m.resolveBackingFile(key)
	if !errors.Is(err, errBackingNotCached) {
		return path, err
	}
	// The node cache installs a durable fill before Get returns, so the file
	// resolves without reading the stream; the fill must not be deferred.
	rc, getErr := m.storage.Get(storage.WithDurableCacheFill(ctx), key)
	if getErr != nil {
		return "", fmt.Errorf("%w: fetch %q: %w", ErrSnapshotBackingUnverified, key, getErr)
	}
	if closeErr := rc.Close(); closeErr != nil {
		return "", fmt.Errorf("%w: fetch %q: %w", ErrSnapshotBackingUnverified, key, closeErr)
	}
	return m.resolveBackingFile(key)
}

// fileDigest returns "sha256:<hex>" for path, hashing each file version once
// per Manager.
func (m *Manager) fileDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:forbidigo // path is a kernel or base image vmmd resolved itself.
	if err != nil {
		return "", fmt.Errorf("%w: open %s: %w", ErrSnapshotBackingUnverified, path, err)
	}
	defer func() { _ = f.Close() }()
	return m.fileDigestOf(f, path)
}

// backingDigestCall is one file version being hashed. Concurrent first boots
// of a base wait for it instead of reading the whole file again.
type backingDigestCall struct {
	done   chan struct{}
	digest string
	err    error
}

// fileDigestOf hashes the file version f has open, once per Manager.
func (m *Manager) fileDigestOf(f *os.File, path string) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("%w: stat %s: %w", ErrSnapshotBackingUnverified, path, err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("%w: no inode for %s", ErrSnapshotBackingUnverified, path)
	}
	key := fileIdentityKey{dev: uint64(st.Dev), ino: st.Ino, size: info.Size()} //nolint:unconvert // Dev is int32 on darwin
	if sec, nsec, ok := fileBirthTime(f); ok {
		key.btimeSec, key.btimeNsec = sec, nsec
	}
	m.backingMu.Lock()
	if digest, ok := m.backingDigests[key]; ok {
		m.backingMu.Unlock()
		return digest, nil
	}
	if call := m.backingHashing[key]; call != nil {
		m.backingMu.Unlock()
		<-call.done
		return call.digest, call.err
	}
	if m.backingHashing == nil {
		m.backingHashing = make(map[fileIdentityKey]*backingDigestCall)
	}
	call := &backingDigestCall{done: make(chan struct{})}
	m.backingHashing[key] = call
	m.backingMu.Unlock()

	recorder := m.digestRecorder()
	if digest, ok := recorder.recorded(f, info.Size()); ok {
		// Hashed by an earlier vmmd process: a restart no longer makes the
		// first wake of every base pay for reading all of it.
		call.digest = digest
	} else {
		if m.beforeBackingHash != nil {
			m.beforeBackingHash(path)
		}
		h := sha256.New()
		if _, err := io.Copy(h, io.NewSectionReader(f, 0, info.Size())); err != nil {
			call.err = fmt.Errorf("%w: hash %s: %w", ErrSnapshotBackingUnverified, path, err)
		} else {
			call.digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
			if err := recorder.record(f, info.Size(), call.digest); err != nil && m.log != nil {
				m.log.Warn("record snapshot backing digest", "path", path, "err", err)
			}
		}
	}
	m.backingMu.Lock()
	delete(m.backingHashing, key)
	if call.err == nil {
		if m.backingDigests == nil {
			m.backingDigests = make(map[fileIdentityKey]string)
		}
		m.backingDigests[key] = call.digest
	}
	m.backingMu.Unlock()
	close(call.done)
	return call.digest, call.err
}

// BackingDigestPrimeInterval is how often RunBackingDigestPrimer looks for
// kernel and base versions this vmmd has not identified yet. A release
// pre-stage replaces bases while the previous vmmd still serves wakes.
const BackingDigestPrimeInterval = 30 * time.Second

// maxPrimedBackingKeys bounds the base keys the primer follows beyond the
// configured generations. A node attaches a few dozen bases at most.
const maxPrimedBackingKeys = 256

// RunBackingDigestPrimer primes digests now and then every interval until ctx
// ends, so a base that changed under this vmmd (a release pre-stage, another
// writer's refresh) is identified before a wake needs it.
func (m *Manager) RunBackingDigestPrimer(ctx context.Context, interval time.Duration) {
	m.PrimeBackingDigests(ctx)
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.PrimeBackingDigests(ctx)
		}
	}
}

// PrimeBackingDigests identifies the kernel, every configured runtime base
// and every base this vmmd has attached that is cached locally, so a restore
// does not hash a ~250 MiB base in its wake path. A version already known
// costs an open and a stat; one recorded by an earlier process costs an
// xattr read. It never advances a cached base's LRU position, so priming
// cannot keep an unused base resident. Best-effort and safe to run
// concurrently with wakes (a concurrent identification waits for this one).
func (m *Manager) PrimeBackingDigests(ctx context.Context) {
	if !m.backingEnabled() {
		return
	}
	for _, key := range m.primedBackingKeys() {
		if ctx.Err() != nil {
			return
		}
		path, ok := m.inspectBackingFile(key)
		if !ok {
			m.forgetBackingKey(key) // evicted; its next attach notes it again
			continue
		}
		if _, err := m.fileDigest(path); err != nil {
			m.log.Warn("prime snapshot backing digest", "key", key, "err", err)
		}
	}
}

// primedBackingKeys is the kernel, the configured bases and the noted ones.
func (m *Manager) primedBackingKeys() []string {
	// baseGenerations is fixed at startup (WithBaseGenerations) and read
	// without a lock elsewhere; baseGenerationMu would only queue this behind
	// a refresh download.
	keys := []string{m.paths.Kernel}
	seen := map[string]struct{}{m.paths.Kernel: {}}
	for baseKey := range m.baseGenerations {
		if _, dup := seen[baseKey]; !dup {
			seen[baseKey] = struct{}{}
			keys = append(keys, baseKey)
		}
	}
	m.backingMu.Lock()
	for baseKey := range m.backingKeys {
		if _, dup := seen[baseKey]; !dup {
			seen[baseKey] = struct{}{}
			keys = append(keys, baseKey)
		}
	}
	m.backingMu.Unlock()
	return keys
}

// noteBackingKey remembers a base key a wake on this node attached or
// verified, so the primer follows its file version from now on.
func (m *Manager) noteBackingKey(key string) {
	if key == "" {
		return
	}
	m.backingMu.Lock()
	defer m.backingMu.Unlock()
	if m.backingKeys == nil {
		m.backingKeys = make(map[string]struct{})
	}
	if len(m.backingKeys) < maxPrimedBackingKeys {
		m.backingKeys[key] = struct{}{}
	}
}

func (m *Manager) forgetBackingKey(key string) {
	m.backingMu.Lock()
	delete(m.backingKeys, key)
	m.backingMu.Unlock()
}

// inspectBackingFile is resolveBackingFile for the primer: it never touches
// the cache entry's LRU position and never fetches.
func (m *Manager) inspectBackingFile(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	if filepath.IsAbs(key) {
		return key, true
	}
	path, local, err := storage.LocalPathWithoutTouch(m.storage, key)
	return path, err == nil && local
}

// backingFiles are the kernel and base a wake attaches, held open so their
// identity is the version the VM booted with even if a refresh replaces the
// files before they are hashed.
type backingFiles struct {
	kernel, base         *os.File
	kernelPath, basePath string
}

func (m *Manager) openBacking(baseKey string, resolve func(string) (string, error)) (backingFiles, error) {
	kernelPath, err := resolve(m.paths.Kernel)
	if err != nil {
		return backingFiles{}, err
	}
	basePath, err := resolve(baseKey)
	if err != nil {
		return backingFiles{}, err
	}
	kernel, err := os.Open(kernelPath) //nolint:forbidigo // vmmd's own kernel image.
	if err != nil {
		return backingFiles{}, fmt.Errorf("%w: open %s: %w", ErrSnapshotBackingUnverified, kernelPath, err)
	}
	base, err := os.Open(basePath) //nolint:forbidigo // a base image vmmd resolved itself.
	if err != nil {
		_ = kernel.Close()
		return backingFiles{}, fmt.Errorf("%w: open %s: %w", ErrSnapshotBackingUnverified, basePath, err)
	}
	return backingFiles{kernel: kernel, base: base, kernelPath: kernelPath, basePath: basePath}, nil
}

func (b backingFiles) close() {
	_ = b.kernel.Close()
	_ = b.base.Close()
}

func (m *Manager) identifyBackingFiles(b backingFiles) (BackingIdentity, error) {
	kernel, err := m.fileDigestOf(b.kernel, b.kernelPath)
	if err != nil {
		return BackingIdentity{}, err
	}
	base, err := m.fileDigestOf(b.base, b.basePath)
	if err != nil {
		return BackingIdentity{}, err
	}
	return BackingIdentity{Version: backingIdentityVersion, Kernel: kernel, Base: base, Timer: strings.TrimSpace(guestTimerProfile)}, nil
}

// rememberInstanceBacking records the images a VM just booted or restored
// with, so a later capture describes the running VM rather than whatever the
// logical base key resolves to by then. Best-effort: an unidentified VM
// produces captures without an identity, which ADR-510 refuses to restore.
//
// The files are opened now, pinning the versions the VM booted with, and
// hashed in the background: the first boot of a base version reads all of
// it, which took 48 s for the 2.3 GB agent base on a native host. A wake no
// longer waits for that; whatever needs the identity waits for it
// (awaitInstanceBacking).
func (m *Manager) rememberInstanceBacking(instance, baseKey string) {
	if !m.backingEnabled() {
		return
	}
	m.noteBackingKey(baseKey)
	files, err := m.openBacking(baseKey, m.resolveBackingFile)
	if err != nil {
		m.recordInstanceBacking(instance, nil, BackingIdentity{}, err)
		return
	}
	done := make(chan struct{})
	m.backingMu.Lock()
	if m.backingPending == nil {
		m.backingPending = make(map[string]chan struct{})
	}
	m.backingPending[instance] = done
	delete(m.instanceBacking, instance)
	m.backingMu.Unlock()
	go func() {
		defer close(done)
		started := time.Now()
		identity, err := m.identifyBackingFiles(files)
		files.close()
		if elapsed := time.Since(started); elapsed >= time.Second {
			m.log.Info("snapshot backing identified", "instance", instance, "base", files.basePath,
				"duration_ms", elapsed.Milliseconds())
		}
		m.recordInstanceBacking(instance, done, identity, err)
	}()
}

// recordInstanceBacking stores an instance's identity, or forgets it on err.
// done is the pending computation it ends; a later wake of the same instance
// name supersedes it, and then nothing is recorded.
func (m *Manager) recordInstanceBacking(instance string, done chan struct{}, identity BackingIdentity, err error) {
	m.backingMu.Lock()
	if done != nil {
		if m.backingPending[instance] != done {
			m.backingMu.Unlock()
			return
		}
		delete(m.backingPending, instance)
	}
	if m.instanceBacking == nil {
		m.instanceBacking = make(map[string]BackingIdentity)
	}
	if err == nil {
		m.instanceBacking[instance] = identity
	} else {
		delete(m.instanceBacking, instance)
	}
	m.backingMu.Unlock()
	if err != nil {
		m.log.Warn("snapshot backing identity unavailable; captures of this instance will not be restorable",
			"instance", instance, "err", err)
		return
	}
	// A guest that came up before its hash finished was recorded for
	// adoption without the identity; rewrite its block so a restart keeps
	// it. A guest not yet live is skipped and recorded after bring-up.
	m.markAdoptionDirty(instance)
}

// awaitInstanceBacking returns the instance's identity once any computation
// rememberInstanceBacking started for it has finished, or false when it has
// none or ctx ends first.
func (m *Manager) awaitInstanceBacking(ctx context.Context, instance string) (BackingIdentity, bool) {
	m.backingMu.Lock()
	done := m.backingPending[instance]
	m.backingMu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return BackingIdentity{}, false
		}
	}
	m.backingMu.Lock()
	defer m.backingMu.Unlock()
	identity, ok := m.instanceBacking[instance]
	return identity, ok
}

// writeSnapshotBacking stores the running VM's identity next to a capture.
// Best-effort: a capture without it is refused at restore and cold-boots.
func (m *Manager) writeSnapshotBacking(ctx context.Context, instance, memKey string) {
	m.writeSnapshotBackingTo(ctx, m.storage, instance, memKey)
}

// writeSnapshotBackingTo writes the identity through store (a sealed
// capture's encrypting backend, or the shared one).
func (m *Manager) writeSnapshotBackingTo(ctx context.Context, store storage.StorageBackend, instance, memKey string) {
	if !m.backingEnabled() {
		return
	}
	key := state.SnapshotBackingKey(state.Snapshot{StorageKey: memKey})
	identity, ok := m.awaitInstanceBacking(ctx, instance)
	if key == "" || !ok {
		m.log.Warn("snapshot captured without a backing identity; it will cold-boot instead of restoring",
			"instance", instance, "storage_key", memKey)
		return
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return
	}
	if err := store.Put(ctx, key, bytes.NewReader(body)); err != nil {
		m.log.Warn("write snapshot backing identity", "instance", instance, "key", key, "err", err)
	}
}

// BackingCheckTimings attribute the ADR-510 pre-restore backing check, which
// runs after the network is up and before the VMM restore window (ADR-192
// prepare timings). TotalMs is the whole check; the parts are disjoint.
type BackingCheckTimings struct {
	TotalMs int64
	// SidecarMs reads the capture's …/backing identity: a node-local cache
	// hit, or a read from shared storage.
	SidecarMs int64
	// FetchMs resolves the kernel and base this wake would attach, fetching
	// an evicted base into the node cache.
	FetchMs int64
	// HashMs identifies them: a memoized or recorded digest, a wait for a
	// concurrent identification, or a full SHA-256 read of the file.
	HashMs int64
}

// attrs are the wake ok log fields; empty when no check ran.
func (t BackingCheckTimings) attrs() []any {
	if t == (BackingCheckTimings{}) {
		return nil
	}
	return []any{"backing_check_ms", t.TotalMs, "backing_sidecar_ms", t.SidecarMs,
		"backing_fetch_ms", t.FetchMs, "backing_hash_ms", t.HashMs}
}

// verifySnapshotBacking returns nil when the capture's recorded identity
// matches the images this wake would attach, ErrSnapshotBackingUnverified
// when either side is unknown, and ErrSnapshotBackingChanged on a mismatch.
func (m *Manager) verifySnapshotBacking(ctx context.Context, snap *Snapshot, baseKey string) error {
	_, err := m.checkSnapshotBacking(ctx, snap, baseKey)
	return err
}

// checkSnapshotBacking is verifySnapshotBacking with the time each part took.
func (m *Manager) checkSnapshotBacking(ctx context.Context, snap *Snapshot, baseKey string) (timings BackingCheckTimings, err error) {
	if !m.backingEnabled() || snap == nil {
		return BackingCheckTimings{}, nil
	}
	started := time.Now()
	defer func() { timings.TotalMs = time.Since(started).Milliseconds() }()
	m.noteBackingKey(baseKey)
	key := state.SnapshotBackingKey(state.Snapshot{StorageKey: snap.StorageKey})
	if key == "" {
		return timings, fmt.Errorf("%w: no backing identity location for %q", ErrSnapshotBackingUnverified, snap.StorageKey)
	}
	store := m.storage
	if snap.Storage != nil {
		store = snap.Storage
	} else if native, ok := m.vmm.(interface {
		nativeRuntimeSnapshotStore(string) storage.StorageBackend
	}); ok {
		// A native runtime capture's sidecar lives in the tree only vmmd writes.
		if b := native.nativeRuntimeSnapshotStore(key); b != nil {
			store = b
		}
	}
	phase := time.Now()
	recorded, err := readSnapshotBacking(ctx, store, key)
	timings.SidecarMs = time.Since(phase).Milliseconds()
	if err != nil {
		return timings, err
	}
	phase = time.Now()
	files, err := m.openBacking(baseKey, func(key string) (string, error) { return m.fetchBackingFile(ctx, key) })
	timings.FetchMs = time.Since(phase).Milliseconds()
	if err != nil {
		// This node's images, not the capture, could not be identified.
		return timings, fmt.Errorf("%w: %w", errBackingCheckFailed, err)
	}
	phase = time.Now()
	current, err := m.identifyBackingFiles(files)
	files.close()
	timings.HashMs = time.Since(phase).Milliseconds()
	if err != nil {
		return timings, fmt.Errorf("%w: %w", errBackingCheckFailed, err)
	}
	if current.Kernel != recorded.Kernel || current.Base != recorded.Base {
		return timings, fmt.Errorf("%w: captured kernel=%s base=%s, now kernel=%s base=%s", ErrSnapshotBackingChanged,
			recorded.Kernel, recorded.Base, current.Kernel, current.Base)
	}
	if current.Timer != recorded.Timer {
		return timings, fmt.Errorf("%w: captured guest timer %q, now %q", ErrSnapshotBackingChanged, recorded.Timer, current.Timer)
	}
	return timings, nil
}

// readSnapshotBacking reads and validates a capture's recorded identity.
func readSnapshotBacking(ctx context.Context, store storage.StorageBackend, key string) (BackingIdentity, error) {
	rc, err := store.Get(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return BackingIdentity{}, fmt.Errorf("%w: read %s: %w", ErrSnapshotBackingUnverified, key, err)
	}
	if err != nil {
		return BackingIdentity{}, fmt.Errorf("%w: read %s: %w: %w", ErrSnapshotBackingUnverified, key, errBackingCheckFailed, err)
	}
	var recorded BackingIdentity
	decodeErr := json.NewDecoder(io.LimitReader(rc, 4096)).Decode(&recorded)
	_ = rc.Close()
	if decodeErr != nil || !recorded.complete() {
		return BackingIdentity{}, fmt.Errorf("%w: invalid identity at %s", ErrSnapshotBackingUnverified, key)
	}
	return recorded, nil
}
