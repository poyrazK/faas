package fcvm

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"sync"

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
// (another node, a vmmd restart, a legacy key) or any changed input — a
// rotated secret, new env, a different resolver — writes exactly as before.

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
