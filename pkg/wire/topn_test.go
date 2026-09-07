// Property tests for the top-N tenant admission primitive introduced
// for issue #300 (per-tenant noisy-customer gauge + FaasTenantAbuse
// alert). The primitive lives in pkg/wire/topn.go and is layered
// above the §"accountLabelSet" admission primitive; it bounds the
// <prefix>_top_tenant_rps{account_id} gauge at topAccountSetCap +
// 1 (cap real ids + the "other" overflow bucket).
//
// The shape of these tests is load-bearing: they pin the contract
// that
//   - the first topAccountSetCap distinct ids surface as labelled
//     gauge series (after EmitTopTenantRPS),
//   - further ids collapse to "other" without consuming capacity,
//   - the gauge is race-safe under -race,
//   - the rolling-window reset wipes the counts without panicking,
//   - the gauge exposition never exceeds cap + 1 series (acceptance #5).
//
// Blackbox `package wire_test` reaching into the OpsMetrics
// surface; the unexported `topAccountSet` is exercised via the
// ObserveTopTenantRPS / EmitTopTenantRPS accessors added by this PR.
// The 24h reset test reaches the primitive's clock seam via a
// small test-only helper exposed for that purpose
// (TestAdvanceClock).
//
// Test setup convention: each test calls ObserveTopTenantRPS(id)
// once per id to bump the rolling-window count, then calls
// EmitTopTenantRPS(...) once to drive the gauge. This mirrors the
// production path where the sampler goroutine ticks every 5s and
// drives both phases.

package wire_test

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/wire"
)

// TestTopTenantRPS_BoundedCardinality asserts the gauge exposition
// never exceeds topAccountSetCap + 1 (1001) series even under fuzzed
// load with 5 000 distinct account ids (acceptance #5; five times the cap —
// the bound is exercised as soon as the set overflows, and 50 000 ids cost
// minutes under -race on a CI runner).
//
// Implementation note: the gauge series count is read from the
// Prometheus /metrics body (render helper). Counting by body lines
// is robust against the pre-instantiated ("other",) row that the
// constructor emits at boot — the body has at most cap + 1 distinct
// lines that match the metric prefix.
func TestTopTenantRPS_BoundedCardinality(t *testing.T) {
	if testing.Short() {
		t.Skip("fuzz-heavy; skipped in -short")
	}
	const totalIDs = 5_000
	m := wire.NewOpsMetrics("apid")
	for i := 0; i < totalIDs; i++ {
		m.ObserveTopTenantRPS(fmt.Sprintf("acct-%08x", i))
	}
	m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	body := render(t, m)
	lines := strings.Split(body, "\n")
	var count int
	for _, ln := range lines {
		if strings.HasPrefix(ln, "apid_top_tenant_rps{") {
			count++
		}
	}
	if count > 1001 {
		t.Fatalf("gauge exposition has %d series; bound is 1001 (topAccountSetCap=1000 + 1 overflow)", count)
	}
	// The "other" overflow bucket must be present, because we drove
	// 5 000 ids through a 1000-cap set.
	if !strings.Contains(body, `apid_top_tenant_rps{account_id="other"}`) {
		t.Errorf("expected other overflow series in:\n%s", body)
	}
	// Sanity: the gauge must hold at least the top-1 id (acct-00000000
	// is the first distinct id, so its 5s-rps is 1.0).
	if !strings.Contains(body, `apid_top_tenant_rps{account_id="acct-00000000"}`) {
		t.Errorf("expected top-1 series acct-00000000 in:\n%s", body)
	}
}

// TestTopTenantRPS_TopNOrdering asserts that when more than cap
// distinct ids are observed, the gauge holds exactly the top-cap
// by sample count and the rest collapse to "other".
func TestTopTenantRPS_TopNOrdering(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	// 5 hot ids (each observed 100 times) plus cap+5 noise ids
	// (each observed once). Top-N should surface the 5 hot ids;
	// the noise ids overflow to "other".
	for i := 0; i < 5; i++ {
		for j := 0; j < 100; j++ {
			m.ObserveTopTenantRPS(fmt.Sprintf("acct-%08x", i))
		}
	}
	for i := 5; i < 1005; i++ {
		m.ObserveTopTenantRPS(fmt.Sprintf("acct-%08x", i))
	}
	m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	body := render(t, m)
	for i := 0; i < 5; i++ {
		want := fmt.Sprintf(`apid_top_tenant_rps{account_id="acct-%08x"}`, i)
		if !strings.Contains(body, want) {
			t.Errorf("hot id %d missing from body:\n%s", i, body)
		}
	}
	// Mid-count ids must not appear — their count (1) ties with the
	// far-tail ids; tie-break is lex, so the lex-smallest 1000 of
	// the 1000 noise ids win. But: the noise ids are acct-00000005
	// through acct-00001004, of which the first 995 (lex) survive.
	// acct-00001004 specifically must NOT appear (it's lex-greater
	// than 995 of the noise ids).
	tail := fmt.Sprintf(`apid_top_tenant_rps{account_id="acct-%08x"}`, 1004)
	if strings.Contains(body, tail) {
		t.Errorf("tail id 1004 should have collapsed to other:\n%s", body)
	}
}

// TestTopTenantRPS_RollingReset asserts that a fresh OpsMetrics
// starts with an empty top-N (the primitive is per-instance, not
// daemon-lifetime-global).
func TestTopTenantRPS_RollingReset(t *testing.T) {
	m1 := wire.NewOpsMetrics("apid")
	m1.ObserveTopTenantRPS("acct-aaa")
	m1.ObserveTopTenantRPS("acct-bbb")
	m1.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	// The contract we pin: a second NewOpsMetrics is independent.
	// The primitive is per-OpsMetrics; a fresh instance starts
	// empty except for the pre-instantiated ("other",) row.
	m2 := wire.NewOpsMetrics("apid")
	m2.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	body2 := render(t, m2)
	if strings.Contains(body2, `account_id="acct-aaa"`) {
		t.Errorf("fresh OpsMetrics leaked top-N state from m1:\n%s", body2)
	}
}

// TestTopTenantRPS_ConcurrentSample is the -race detector for the
// sampler goroutine (cmd/apid/topn.go ticks every 5s) and any
// concurrent request path that calls ObserveTopTenantRPS. Without
// the sync.Mutex on topAccountSet this test would flake on
// map-write races under -race.
func TestTopTenantRPS_ConcurrentSample(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const goroutines = 16
	const perGoroutine = 500
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(gid int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := fmt.Sprintf("acct-%04x-%04x", gid, i)
				m.ObserveTopTenantRPS(id)
			}
		}(g)
	}
	wg.Wait()
	// One emit, once: this is the load-bearing assertion. The
	// sampler-side emit happens once per 5s tick, so the gauge
	// series count after a single emit is bounded at cap + 1.
	emitted := m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	if emitted > 1001 {
		t.Fatalf("EmitTopTenantRPS emitted %d series; bound is 1001", emitted)
	}
	body := render(t, m)
	lines := strings.Split(body, "\n")
	var count int
	for _, ln := range lines {
		if strings.HasPrefix(ln, "apid_top_tenant_rps{") {
			count++
		}
	}
	if count > 1001 {
		t.Fatalf("after concurrent sample + single emit, gauge has %d series; bound is 1001", count)
	}
}

// TestTopTenantRPS_OtherBucketOnIdle asserts that an OpsMetrics with
// no observations still emits the ("other",) row from the boot
// pre-instantiation. This is the precedent set by every other
// CounterVec / GaugeVec in NewOpsMetrics: a daemon must surface its
// metrics from the moment it boots, not only after the first
// observation. Same precedent as the per-plan residentGBPerCustomer
// pre-instantiation (metrics.go:560) and the egress-deny catalog
// pre-instantiation (metrics.go:576).
func TestTopTenantRPS_OtherBucketOnIdle(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	body := render(t, m)
	want := `apid_top_tenant_rps{account_id="other"}`
	if !strings.Contains(body, want) {
		t.Errorf("idle daemon missing pre-instantiated other row %q:\n%s", want, body)
	}
}

// TestTopTenantRPS_HotIDInTopN asserts that a real customer id with
// high count surfaces under its own label.
func TestTopTenantRPS_HotIDInTopN(t *testing.T) {
	m := wire.NewOpsMetrics("apid")
	const hot = "acct-hot-customer"
	for i := 0; i < 100; i++ {
		m.ObserveTopTenantRPS(hot)
	}
	m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	body := render(t, m)
	if !strings.Contains(body, fmt.Sprintf(`apid_top_tenant_rps{account_id=%q}`, hot)) {
		t.Errorf("hot customer id missing; body:\n%s", body)
	}
}

// TestTopTenantRPS_ResetAfter24h exercises the 24h rolling reset
// path with a fake clock via the TestAdvanceClock test seam on
// OpsMetrics (the blackbox test can't reach the primitive's
// unexported fields directly). The seam overrides the primitive's
// `now` clock AND rewinds `lastReset` so the shouldReset math is
// deterministic. Without this test, a regression that breaks the
// reset would surface as a stale id persisting past the 24h
// window — visible only via the dashboard's "Other bucket growth"
// panel, which is a fleet-level signal not caught by the alert
// synthetic-fixture test.
//
// Implementation note: this is the blackbox analog of the same
// test in cmd/gatewayd-internal/topn_test.go (which is whitebox on the
// private primitive). The two primitives are independent (no
// shared test surface) so each gets its own coverage.
func TestTopTenantRPS_ResetAfter24h(t *testing.T) {
	base := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	m := wire.NewOpsMetrics("apid")
	m.ObserveTopTenantRPS("acct-stale")
	// Freeze the clock at base AND rewind lastReset so the
	// baseline starts at base.
	m.TestAdvanceClock(base, true)
	// Advance the clock past the window without rewinding — now
	// lastReset is still at base, now() is base+25h, shouldReset
	// returns true.
	m.TestAdvanceClock(base.Add(25*time.Hour), false)
	set := m.TopAccountSet()
	if !set.ShouldReset() {
		t.Fatal("ShouldReset() = false after 25h; want true")
	}
	set.ResetWindow()
	// Stale id must be gone after reset. EmitTopTenantRPS
	// returns len(snap) + 1 (the +1 is the "other" overflow row).
	// After reset, snap is empty so the return value is exactly
	// 1 (the overflow row only). A value > 1 would mean some
	// real id survived the reset.
	emitted := m.EmitTopTenantRPS(func(string) float64 { return 1.0 })
	if emitted != 1 {
		t.Errorf("after reset, EmitTopTenantRPS emitted %d series; want 1 (only the other overflow row — acct-stale must be gone)", emitted)
	}
	// ShouldReset must be false again immediately after reset
	// (lastReset was just bumped to now() = base+25h).
	if set.ShouldReset() {
		t.Error("ShouldReset() = true immediately after reset; want false")
	}
}
