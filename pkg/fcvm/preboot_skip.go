package fcvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/onebox-faas/faas/pkg/state"
)

// Skipping unchanged pre-boot writes on restore.
//
// Every restore used to loop-mount the instance's writable drive to rewrite
// secrets.env, env.json, the resolver and workload files (~15-20 ms, about a
// fifth of a warm restore). On a v2 restore that drive is a clone of the drive
// captured at park, which already holds whatever this vmmd wrote for the
// instance the capture came from. vmmd remembers a digest of those writes per
// instance and carries it onto each capture; a restore whose writes hash the
// same skips the mount. Nothing is skipped on a guess: an unknown capture
// (another node, a legacy key) or any changed input — a rotated secret, new
// env, a different resolver — writes exactly as before.
//
// Parks reuse a healthy snapshot instead of capturing again, so after a vmmd
// restart the capture-time record alone would never return: production went
// on mounting every restore. The first restore of a v2 capture this vmmd has
// no record for therefore mounts as before and reads what the staged drive
// already holds. When every file it would write is already there, exactly as
// its own write would leave it, it writes nothing and records the capture, so
// later restores of it skip the mount.

// preBootLedgerMaxCaptures bounds the capture map; each entry is two short
// strings, so the worst case is well under a MiB of vmmd heap.
const preBootLedgerMaxCaptures = 4096

type preBootLedger struct {
	mu        sync.Mutex
	instances map[string]string // instance -> digest of pre-boot files on its drive
	captures  map[string]string // capture mem key -> digest on the captured drive
	order     []string          // capture insertion order, oldest first
}

func newPreBootLedger() *preBootLedger {
	return &preBootLedger{instances: make(map[string]string), captures: make(map[string]string)}
}

func (p *preBootLedger) instanceHas(instance, digest string) {
	if p == nil || instance == "" || digest == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.instances[instance] = digest
}

func (p *preBootLedger) forget(instance string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.instances, instance)
}

// captured records that storageKey's drive was captured from instance. Only
// v2 capture keys carry a drive; a legacy restore stages the pristine app
// layer, which never holds the pre-boot files.
func (p *preBootLedger) captured(instance, storageKey string) {
	if p == nil || state.SnapshotDriveKey(state.Snapshot{StorageKey: storageKey}) == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	digest, ok := p.instances[instance]
	if !ok {
		return
	}
	p.recordLocked(storageKey, digest)
}

// learnable reports whether a restore of storageKey may record what its
// staged drive already holds: only a v2 capture stages a captured drive.
func (p *preBootLedger) learnable(storageKey string) bool {
	return p != nil && state.SnapshotDriveKey(state.Snapshot{StorageKey: storageKey}) != ""
}

// learned records that storageKey's captured drive was found to already hold
// the pre-boot files digest describes (see preBootFilesPresent).
func (p *preBootLedger) learned(storageKey, digest string) {
	if !p.learnable(storageKey) || digest == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordLocked(storageKey, digest)
}

func (p *preBootLedger) recordLocked(storageKey, digest string) {
	if _, seen := p.captures[storageKey]; !seen {
		p.order = append(p.order, storageKey)
		for len(p.order) > preBootLedgerMaxCaptures {
			delete(p.captures, p.order[0])
			p.order = p.order[1:]
		}
	}
	p.captures[storageKey] = digest
}

func (p *preBootLedger) captureHas(storageKey, digest string) bool {
	if p == nil || storageKey == "" || digest == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.captures[storageKey] == digest
}

// preBootDigest accumulates every pre-boot write's label and payload in
// order, length-prefixed so adjacent fields cannot alias. It is never logged:
// secrets feed it.
type preBootDigest struct{ h hash.Hash }

func newPreBootDigest() *preBootDigest { return &preBootDigest{h: sha256.New()} }

func (d *preBootDigest) add(label string, payload []byte) {
	var n [8]byte
	for _, b := range [][]byte{[]byte(label), payload} {
		binary.BigEndian.PutUint64(n[:], uint64(len(b)))
		_, _ = d.h.Write(n[:])
		_, _ = d.h.Write(b)
	}
}

func (d *preBootDigest) sum() string { return hex.EncodeToString(d.h.Sum(nil)) }

// preBootFilesPresent reports whether every writer's file is already on the
// mounted drive exactly as its write would leave it: a regular file owned by
// this process, with the writer's permission bits and byte-identical
// contents. Only then is skipping the writes indistinguishable from doing
// them. The drive is tenant-writable, so paths resolve through os.Root and
// any symlink on the way counts as absent — a restore never reads outside
// the mount, and a planted link only costs the write it would have had.
func preBootFilesPresent(mountRoot string, writers []preBootFileWriter) bool {
	if len(writers) == 0 {
		return false
	}
	root, err := os.OpenRoot(mountRoot)
	if err != nil {
		return false
	}
	defer func() { _ = root.Close() }()
	for _, w := range writers {
		if w.path == "" {
			return false
		}
		target, err := stagedDrivePath(mountRoot, w.path)
		if err != nil {
			return false
		}
		rel, err := filepath.Rel(mountRoot, target)
		if err != nil || !preBootFilePresent(root, rel, w.want, w.mode) {
			return false
		}
	}
	return true
}

func preBootFilePresent(root *os.Root, rel string, want []byte, mode os.FileMode) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	for i := range parts {
		info, err := root.Lstat(filepath.Join(parts[:i+1]...))
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if i < len(parts)-1 && !info.IsDir() {
			return false
		}
		if i == len(parts)-1 && (!info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() ||
			info.Size() != int64(len(want)) || !ownedBySelf(info)) {
			return false
		}
	}
	f, err := root.Open(rel)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	got, err := io.ReadAll(io.LimitReader(f, int64(len(want))+1))
	return err == nil && bytes.Equal(got, want)
}

// ownedBySelf: a file this process creates is owned by its effective uid,
// root in production. A file the guest re-owned would keep its owner through
// a truncating write but not through the resolver's replace, so it counts as
// absent.
func ownedBySelf(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
