// Property tests for the gateway-side topAccountSet primitive
// (cmd/gatewayd/topn.go). This is a private mirror of
// pkg/wire/topn.go's primitive — kept identical in shape so a
// future refactor can collapse both into a single shared
// primitive. The two are NOT yet shared via tests (the ADR-040
// rationale: pkg/gateway can't import pkg/wire without a cycle);
// this test pins the gateway side directly so ordering and
// rps-math regressions are caught at PR time.
//
// Whitebox package (package main, not package main_test) because
// the topAccountSet fields are unexported and the tests need to
// backdate lastReset / override `now` for the reset path. Same
// precedent as pkg/wire/topn_test.go.
//
// Test parity with pkg/wire/topn_test.go:
//   - TestGatewayTopTenant_BoundedCardinality  (acceptance #5 pin)
//   - TestGatewayTopTenant_TopNOrdering
//   - TestGatewayTopTenant_RollingReset
//   - TestGatewayTopTenant_ConcurrentSample    (race-detector)
//   - TestGatewayTopTenant_ResetAfter24h       (fake clock)

package main

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestGatewayTopTenant_BoundedCardinality asserts the primitive's
// topNSnapshot never exceeds topAccountSetCap entries even under
// fuzzed load with 50 000 distinct ids. Mirrors
// pkg/wire/topn_test.go::TestTopTenantRPS_BoundedCardinality.
func TestGatewayTopTenant_BoundedCardinality(t *testing.T) {
	if testing.Short() {
		t.Skip("fuzz-heavy; skipped in -short")
	}
	const totalIDs = 50_000
	set := newTopAccountSet(topAccountSetCap)
	for i := 0; i < totalIDs; i++ {
		set.sample(fmt.Sprintf("app-%08x", i))
	}
	snap := set.topNSnapshot()
	if len(snap) > topAccountSetCap {
		t.Fatalf("topNSnapshot returned %d entries; bound is %d", len(snap), topAccountSetCap)
	}
}

// TestGatewayTopTenant_TopNOrdering asserts the topNSnapshot
// returns the highest-count ids, ties broken by lex ascending.
// Same shape as pkg/wire/topn_test.go::TestTopTenantRPS_TopNOrdering.
func TestGatewayTopTenant_TopNOrdering(t *testing.T) {
	set := newTopAccountSet(topAccountSetCap)
	// 5 hot ids (100 samples each) + cap+5 noise ids (1 sample each).
	// Top-N should surface the 5 hot ids and the lex-smallest 995
	// noise ids; the lex-largest 5 noise ids collapse.
	for i := 0; i < 5; i++ {
		for j := 0; j < 100; j++ {
			set.sample(fmt.Sprintf("app-%08x", i))
		}
	}
	for i := 5; i < topAccountSetCap+5; i++ {
		set.sample(fmt.Sprintf("app-%08x", i))
	}
	snap := set.topNSnapshot()
	// First 5 entries must be the hot ids (count=100), descending.
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf("app-%08x", i)
		if snap[i].ID != want {
			t.Errorf("snap[%d].ID = %q, want %q (hot id)", i, snap[i].ID, want)
		}
		if snap[i].Count != 100 {
			t.Errorf("snap[%d].Count = %d, want 100", i, snap[i].Count)
		}
	}
	// The lex-largest 5 noise ids (app-...01000..app-...01004) must
	// NOT appear in the snapshot — they were demoted below the cap.
	for _, dropID := range []int{1000, 1001, 1002, 1003, 1004} {
		bad := fmt.Sprintf("app-%08x", dropID)
		for _, e := range snap {
			if e.ID == bad {
				t.Errorf("demoted id %s should not appear in snap", bad)
				break
			}
		}
	}
}

// TestGatewayTopTenant_RollingReset asserts that two independent
// topAccountSet instances don't share state (the primitive is
// per-instance, not daemon-lifetime-global). Mirrors the apid-side
// TestTopTenantRPS_RollingReset.
func TestGatewayTopTenant_RollingReset(t *testing.T) {
	s1 := newTopAccountSet(topAccountSetCap)
	s1.sample("app-aaa")
	s1.sample("app-bbb")
	s1.topNSnapshot() // forces a read under s1.mu
	s2 := newTopAccountSet(topAccountSetCap)
	s2.topNSnapshot()
	// s2 must not see app-aaa or app-bbb from s1 — the primitive
	// is per-instance. A regression that shared state via a
	// package-level var would surface here.
	for _, e := range s2.topNSnapshot() {
		if e.ID == "app-aaa" || e.ID == "app-bbb" {
			t.Errorf("fresh topAccountSet leaked state from s1: got %s", e.ID)
		}
	}
}

// TestGatewayTopTenant_ConcurrentSample is the -race detector for
// the sampler goroutine and the per-request observe path
// (Handler.observe → topNSampler.Sample → topAccountSet.sample).
// Mirrors pkg/wire/topn_test.go::TestTopTenantRPS_ConcurrentSample.
func TestGatewayTopTenant_ConcurrentSample(t *testing.T) {
	set := newTopAccountSet(topAccountSetCap)
	const goroutines = 16
	const perGoroutine = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := fmt.Sprintf("app-%04x-%04x", gid, i)
				set.sample(id)
			}
		}(g)
	}
	wg.Wait()
	// After 8000 concurrent samples the snapshot must still be
	// bounded at topAccountSetCap — without the sync.Mutex this
	// would race and surface under -race as a map-write race.
	snap := set.topNSnapshot()
	if len(snap) > topAccountSetCap {
		t.Fatalf("after concurrent sample, snap has %d entries; bound is %d", len(snap), topAccountSetCap)
	}
}

// TestGatewayTopTenant_ResetAfter24h exercises the 24h rolling
// reset path with a fake clock. Mirrors the placeholder in
// pkg/wire/topn_test.go but actually pins the contract here.
//
// Implementation note: the primitive's `now` field is the clock
// seam; we backdate `lastReset` and override `now` to advance
// past topAccountWindow. Without this test, a regression that
// breaks the reset would surface as a stale id persisting past
// the 24h window — visible only via the dashboard's "Other
// bucket growth" panel, which is a fleet-level signal and not
// caught by the alert test.
func TestGatewayTopTenant_ResetAfter24h(t *testing.T) {
	// Construct the primitive with a frozen clock at base so
	// lastReset = base. The default newTopAccountSet uses
	// time.Now(); we override it AFTER construction to a known
	// base so the math below is deterministic.
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	set := newTopAccountSet(topAccountSetCap)
	set.now = func() time.Time { return base }
	set.lastReset = base // rewind to base; default constructor sets it to time.Now()
	set.sample("app-stale")
	// Advance 25h past the window.
	set.now = func() time.Time { return base.Add(25 * time.Hour) }
	if !set.shouldReset() {
		t.Fatal("shouldReset() = false after 25h; want true")
	}
	set.resetWindow()
	// Stale id must be gone after reset.
	for _, e := range set.topNSnapshot() {
		if e.ID == "app-stale" {
			t.Errorf("stale id app-stale persists past 24h reset")
		}
	}
	// shouldReset must be false again immediately after reset.
	if set.shouldReset() {
		t.Error("shouldReset() = true immediately after reset; want false")
	}
}
