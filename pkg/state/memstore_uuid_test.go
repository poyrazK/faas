package state

import "testing"

func TestMemNewUUIDReturnsUniqueIDs(t *testing.T) {
	seen := make(map[[16]byte]struct{}, 257)
	for i := 0; i < 257; i++ {
		id := memNewUUID()
		if _, ok := seen[id]; ok {
			t.Fatalf("memNewUUID returned a duplicate ID at iteration %d: %x", i, id)
		}
		seen[id] = struct{}{}
	}
}
