package gateway

import "testing"

// A sealed cached entry is pinned as is, with no copy; an unsealed one is
// sealed on a private copy and the cached entry is left untouched.
func TestEdgeRuleCachePinnableHost(t *testing.T) {
	c := NewEdgeRuleCache(4)
	sealed := &HostEntry{Route: []EdgeRuleResolved{{ID: "r1"}}}
	if err := sealed.SealPolicy(); err != nil {
		t.Fatal(err)
	}
	c.Put("sealed.example.com", sealed)
	c.Put("unsealed.example.com", &HostEntry{Route: []EdgeRuleResolved{{ID: "r2"}}})

	first, ok, err := c.PinnableHost("sealed.example.com")
	if err != nil || !ok || first.snapshot != sealed.snapshot {
		t.Fatalf("sealed host = %v, %t, %v; want the cached seal", first, ok, err)
	}
	if allocs := testing.AllocsPerRun(100, func() { _, _, _ = c.PinnableHost("sealed.example.com") }); allocs != 0 {
		t.Fatalf("pinning a sealed host allocated %.0f times, want 0", allocs)
	}

	pinned, ok, err := c.PinnableHost("unsealed.example.com")
	if err != nil || !ok || pinned.snapshot == nil || pinned.snapshot.entry.Route[0].ID != "r2" {
		t.Fatalf("unsealed host = %v, %t, %v; want a sealed copy of r2", pinned, ok, err)
	}
	if cached, _ := c.getEntry("unsealed.example.com"); cached.snapshot != nil || cached == pinned {
		t.Fatal("pinning sealed the shared cached entry")
	}

	if entry, ok, err := c.PinnableHost("missing.example.com"); entry != nil || ok || err != nil {
		t.Fatalf("missing host = %v, %t, %v; want a miss", entry, ok, err)
	}
}
