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
// touch rewrites it on every read.
type fileIdentityKey struct {
	dev, ino uint64
	size     int64
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

	if m.beforeBackingHash != nil {
		m.beforeBackingHash(path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(f, 0, info.Size())); err != nil {
		call.err = fmt.Errorf("%w: hash %s: %w", ErrSnapshotBackingUnverified, path, err)
	} else {
		call.digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
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

// PrimeBackingDigests hashes the kernel and every configured runtime base
// that is already cached locally, so the first restore after a vmmd start
// does not pay for hashing a ~250 MiB base in its wake path. Best-effort and
// safe to run concurrently with wakes (a race only duplicates the hash).
func (m *Manager) PrimeBackingDigests(ctx context.Context) {
	if !m.backingEnabled() {
		return
	}
	// baseGenerations is fixed at startup (WithBaseGenerations) and read
	// without a lock elsewhere; baseGenerationMu would only queue this behind
	// a refresh download.
	keys := []string{m.paths.Kernel}
	for baseKey := range m.baseGenerations {
		keys = append(keys, baseKey)
	}
	for _, key := range keys {
		if ctx.Err() != nil {
			return
		}
		path, err := m.resolveBackingFile(key)
		if err != nil {
			continue // not cached yet; the first wake or refresh identifies it
		}
		if _, err := m.fileDigest(path); err != nil {
			m.log.Warn("prime snapshot backing digest", "key", key, "err", err)
		}
	}
}

// identifyBacking identifies the kernel and base, resolving each key with
// resolve.
func (m *Manager) identifyBacking(baseKey string, resolve func(string) (string, error)) (BackingIdentity, error) {
	files, err := m.openBacking(baseKey, resolve)
	if err != nil {
		return BackingIdentity{}, err
	}
	defer files.close()
	return m.identifyBackingFiles(files)
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

// verifySnapshotBacking returns nil when the capture's recorded identity
// matches the images this wake would attach, ErrSnapshotBackingUnverified
// when either side is unknown, and ErrSnapshotBackingChanged on a mismatch.
func (m *Manager) verifySnapshotBacking(ctx context.Context, snap *Snapshot, baseKey string) error {
	if !m.backingEnabled() || snap == nil {
		return nil
	}
	key := state.SnapshotBackingKey(state.Snapshot{StorageKey: snap.StorageKey})
	if key == "" {
		return fmt.Errorf("%w: no backing identity location for %q", ErrSnapshotBackingUnverified, snap.StorageKey)
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
	rc, err := store.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: read %s: %w", ErrSnapshotBackingUnverified, key, err)
	}
	var recorded BackingIdentity
	decodeErr := json.NewDecoder(io.LimitReader(rc, 4096)).Decode(&recorded)
	_ = rc.Close()
	if decodeErr != nil || !recorded.complete() {
		return fmt.Errorf("%w: invalid identity at %s", ErrSnapshotBackingUnverified, key)
	}
	current, err := m.identifyBacking(baseKey, func(key string) (string, error) { return m.fetchBackingFile(ctx, key) })
	if err != nil {
		return err
	}
	if current.Kernel != recorded.Kernel || current.Base != recorded.Base {
		return fmt.Errorf("%w: captured kernel=%s base=%s, now kernel=%s base=%s", ErrSnapshotBackingChanged,
			recorded.Kernel, recorded.Base, current.Kernel, current.Base)
	}
	if current.Timer != recorded.Timer {
		return fmt.Errorf("%w: captured guest timer %q, now %q", ErrSnapshotBackingChanged, recorded.Timer, current.Timer)
	}
	return nil
}
