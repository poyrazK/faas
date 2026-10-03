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
	"syscall"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// backingIdentityVersion versions the sidecar document.
const backingIdentityVersion = 1

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
}

func (b BackingIdentity) complete() bool {
	return b.Version == backingIdentityVersion && b.Kernel != "" && b.Base != ""
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
		return "", fmt.Errorf("%w: resolve %q: %v", ErrSnapshotBackingUnverified, key, err)
	}
	if !local {
		return "", fmt.Errorf("%w: %q is not cached locally", ErrSnapshotBackingUnverified, key)
	}
	return path, nil
}

// fileDigest returns "sha256:<hex>" for path, hashing each file version once
// per Manager.
func (m *Manager) fileDigest(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("%w: stat %s: %v", ErrSnapshotBackingUnverified, path, err)
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
	m.backingMu.Unlock()

	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("%w: open %s: %v", ErrSnapshotBackingUnverified, path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("%w: hash %s: %v", ErrSnapshotBackingUnverified, path, err)
	}
	digest := "sha256:" + hex.EncodeToString(h.Sum(nil))
	m.backingMu.Lock()
	if m.backingDigests == nil {
		m.backingDigests = make(map[fileIdentityKey]string)
	}
	m.backingDigests[key] = digest
	m.backingMu.Unlock()
	return digest, nil
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

// currentBacking identifies the kernel and base a wake would attach now.
func (m *Manager) currentBacking(baseKey string) (BackingIdentity, error) {
	kernelPath, err := m.resolveBackingFile(m.paths.Kernel)
	if err != nil {
		return BackingIdentity{}, err
	}
	basePath, err := m.resolveBackingFile(baseKey)
	if err != nil {
		return BackingIdentity{}, err
	}
	kernel, err := m.fileDigest(kernelPath)
	if err != nil {
		return BackingIdentity{}, err
	}
	base, err := m.fileDigest(basePath)
	if err != nil {
		return BackingIdentity{}, err
	}
	return BackingIdentity{Version: backingIdentityVersion, Kernel: kernel, Base: base}, nil
}

// rememberInstanceBacking records the images a VM just booted or restored
// with, so a later capture describes the running VM rather than whatever the
// logical base key resolves to by then. Best-effort: an unidentified VM
// produces captures without an identity, which ADR-510 refuses to restore.
func (m *Manager) rememberInstanceBacking(instance, baseKey string) {
	if !m.backingEnabled() {
		return
	}
	identity, err := m.currentBacking(baseKey)
	m.backingMu.Lock()
	defer m.backingMu.Unlock()
	if m.instanceBacking == nil {
		m.instanceBacking = make(map[string]BackingIdentity)
	}
	if err != nil {
		delete(m.instanceBacking, instance)
		m.log.Warn("snapshot backing identity unavailable; captures of this instance will not be restorable",
			"instance", instance, "err", err)
		return
	}
	m.instanceBacking[instance] = identity
}

// writeSnapshotBacking stores the running VM's identity next to a capture.
// Best-effort: a capture without it is refused at restore and cold-boots.
func (m *Manager) writeSnapshotBacking(ctx context.Context, instance, memKey string) {
	if !m.backingEnabled() {
		return
	}
	key := state.SnapshotBackingKey(state.Snapshot{StorageKey: memKey})
	m.backingMu.Lock()
	identity, ok := m.instanceBacking[instance]
	m.backingMu.Unlock()
	if key == "" || !ok {
		m.log.Warn("snapshot captured without a backing identity; it will cold-boot instead of restoring",
			"instance", instance, "storage_key", memKey)
		return
	}
	body, err := json.Marshal(identity)
	if err != nil {
		return
	}
	if err := m.storage.Put(ctx, key, bytes.NewReader(body)); err != nil {
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
	rc, err := m.storage.Get(ctx, key)
	if err != nil {
		return fmt.Errorf("%w: read %s: %v", ErrSnapshotBackingUnverified, key, err)
	}
	var recorded BackingIdentity
	decodeErr := json.NewDecoder(io.LimitReader(rc, 4096)).Decode(&recorded)
	_ = rc.Close()
	if decodeErr != nil || !recorded.complete() {
		return fmt.Errorf("%w: invalid identity at %s", ErrSnapshotBackingUnverified, key)
	}
	current, err := m.currentBacking(baseKey)
	if err != nil {
		return err
	}
	if current.Kernel != recorded.Kernel || current.Base != recorded.Base {
		return fmt.Errorf("%w: captured kernel=%s base=%s, now kernel=%s base=%s", ErrSnapshotBackingChanged,
			recorded.Kernel, recorded.Base, current.Kernel, current.Base)
	}
	return nil
}
