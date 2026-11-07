// adr: 570
package gateway

import (
	"bytes"
	"encoding/json"
	"hash/maphash"
	"sync"

	"github.com/onebox-faas/faas/pkg/lruutil"
)

// publicRoutingFreezes shares one decoded copy of each distinct public routing
// snapshot encoding, as appFreezes does for app policies. A request used to
// round-trip its snapshot through JSON for a private copy and to encode and
// hash its proof document; requests with the same encoding now share the
// decoded snapshot and, per traffic policy revision, the proof digest.
//
// A shared snapshot is read-only: each request gets a value copy, selection
// writes only that copy's top-level fields, and Weights is only read.
type publicRoutingFreezes struct {
	once    sync.Once
	seed    maphash.Seed
	entries *lruutil.LRU[uint64, *publicRoutingFreeze]
}

const (
	// publicRoutingFreezeEntries bounds the shared snapshots; a gateway sees
	// one per serving (app, lane, routing input) combination.
	publicRoutingFreezeEntries = 1024
	// A snapshot remembers at most publicRoutingProofMemoEntries proofs, by
	// traffic policy revisions of at most publicRoutingProofMemoKeyBytes.
	publicRoutingProofMemoEntries  = 8
	publicRoutingProofMemoKeyBytes = 256
)

type publicRoutingFreeze struct {
	encoded []byte
	frozen  PublicRoutingSnapshot

	proofsMu sync.Mutex
	proofs   map[string]string
}

// seal returns the decoded snapshot for encoded, from the cache when an
// identical encoding is there.
func (c *publicRoutingFreezes) seal(encoded []byte) (*publicRoutingFreeze, error) {
	c.once.Do(func() {
		c.seed = maphash.MakeSeed()
		c.entries = lruutil.New[uint64, *publicRoutingFreeze](publicRoutingFreezeEntries)
	})
	key := maphash.Bytes(c.seed, encoded)
	if cached, ok := c.entries.Get(key); ok && bytes.Equal(cached.encoded, encoded) {
		return cached, nil
	}
	sealed := &publicRoutingFreeze{encoded: encoded}
	if err := json.Unmarshal(encoded, &sealed.frozen); err != nil {
		return nil, err
	}
	c.entries.Put(key, sealed)
	return sealed, nil
}

// proof is the effective policy revision of a request routed by this
// snapshot under the traffic policy revision base: the digest of
// {"Base":base,"Routing":<snapshot>}.
func (f *publicRoutingFreeze) proof(base string) (string, error) {
	f.proofsMu.Lock()
	digest, ok := f.proofs[base]
	f.proofsMu.Unlock()
	if ok {
		return digest, nil
	}
	encoded, err := json.Marshal(struct {
		Base    string
		Routing json.RawMessage
	}{base, f.encoded})
	if err != nil {
		return "", err
	}
	digest = policyDigest(encoded)
	if len(base) <= publicRoutingProofMemoKeyBytes {
		f.proofsMu.Lock()
		if f.proofs == nil || len(f.proofs) >= publicRoutingProofMemoEntries {
			f.proofs = make(map[string]string, publicRoutingProofMemoEntries)
		}
		f.proofs[base] = digest
		f.proofsMu.Unlock()
	}
	return digest, nil
}
