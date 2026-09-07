package instancestats

import (
	"testing"
	"time"
)

func TestReader_Empty(t *testing.T) {
	r := NewReader()
	if got := r.SnapshotAll(); got != nil {
		t.Errorf("SnapshotAll on empty Reader = %v, want nil", got)
	}
	if got := r.SnapshotForApp("app1"); got != nil {
		t.Errorf("SnapshotForApp on empty Reader = %v, want nil", got)
	}
	if _, ok := r.SnapshotForInstance("i-1"); ok {
		t.Error("SnapshotForInstance on empty Reader returned ok=true")
	}
}

func TestReader_ReplaceSnapshotAllReturnsRows(t *testing.T) {
	r := NewReader()
	now := time.Now()
	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-2", NodeID: "n1", SampledAt: now},
		{AppID: "app1", InstanceID: "i-1", NodeID: "n1", SampledAt: now},
		{AppID: "app2", InstanceID: "i-3", NodeID: "n1", SampledAt: now},
	})
	got := r.SnapshotAll()
	if len(got) != 3 {
		t.Fatalf("SnapshotAll len = %d, want 3", len(got))
	}
	// Deterministic (app, instance) order. Expected: app1/i-1,
	// app1/i-2, app2/i-3.
	if got[0].InstanceID != "i-1" || got[1].InstanceID != "i-2" || got[2].InstanceID != "i-3" {
		t.Errorf("SnapshotAll order = %s, %s, %s; want i-1, i-2, i-3", got[0].InstanceID, got[1].InstanceID, got[2].InstanceID)
	}
}

func TestReader_ReplaceSnapshotForAppFilters(t *testing.T) {
	r := NewReader()
	now := time.Now()
	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-1", NodeID: "n1", SampledAt: now},
		{AppID: "app2", InstanceID: "i-2", NodeID: "n1", SampledAt: now},
		{AppID: "app1", InstanceID: "i-3", NodeID: "n1", SampledAt: now},
	})
	got := r.SnapshotForApp("app1")
	if len(got) != 2 {
		t.Fatalf("SnapshotForApp app1 len = %d, want 2", len(got))
	}
	ids := []string{got[0].InstanceID, got[1].InstanceID}
	want := []string{"i-1", "i-3"}
	for i := range want {
		if ids[i] != want[i] {
			t.Errorf("SnapshotForApp app1[%d] = %q, want %q", i, ids[i], want[i])
		}
	}
}

func TestReader_ReplaceSnapshotForInstance(t *testing.T) {
	r := NewReader()
	now := time.Now()
	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-1", NodeID: "n1", SampledAt: now, CPUPct: 50, RSSMB: 256},
	})
	got, ok := r.SnapshotForInstance("i-1")
	if !ok {
		t.Fatal("SnapshotForInstance i-1: not found")
	}
	if got.CPUPct != 50 || got.RSSMB != 256 {
		t.Errorf("SnapshotForInstance i-1 = %+v, want CPUPct=50 RSSMB=256", got)
	}
	if _, ok := r.SnapshotForInstance("nope"); ok {
		t.Error("SnapshotForInstance nope: found, want not found")
	}
}

func TestReader_RequestsPerSecondAggregatesAndResetsSafely(t *testing.T) {
	r := NewReader()
	base := time.Unix(1_700_000_000, 0)
	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-1", SampledAt: base, RequestCountTotal: 100, RequestCountValid: true},
		{AppID: "app1", InstanceID: "i-2", SampledAt: base, RequestCountTotal: 40, RequestCountValid: true},
	})
	if _, ok := r.RequestsPerSecond("app1"); ok {
		t.Fatal("RequestsPerSecond returned a signal for a first sample")
	}

	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-1", SampledAt: base.Add(time.Second), RequestCountTotal: 130, RequestCountValid: true},
		{AppID: "app1", InstanceID: "i-2", SampledAt: base.Add(time.Second), RequestCountTotal: 50, RequestCountValid: true},
	})
	got, ok := r.RequestsPerSecond("app1")
	if !ok || got != 40 {
		t.Fatalf("RequestsPerSecond(app1) = (%v, %v), want (40, true)", got, ok)
	}

	// A vmmd restart / instance recreation resets its cumulative counter.
	// That sample establishes a new baseline and must not create a burst.
	r.Replace([]InstanceStat{
		{AppID: "app1", InstanceID: "i-1", SampledAt: base.Add(2 * time.Second), RequestCountTotal: 2, RequestCountValid: true},
		{AppID: "app1", InstanceID: "i-2", SampledAt: base.Add(2 * time.Second), RequestCountTotal: 60, RequestCountValid: true},
	})
	got, ok = r.RequestsPerSecond("app1")
	if !ok || got != 10 {
		t.Fatalf("RequestsPerSecond(app1) after one counter reset = (%v, %v), want (10, true)", got, ok)
	}
}

func TestReader_DeterministicOrdering(t *testing.T) {
	r := NewReader()
	now := time.Now()
	// Insert in deliberately scrambled order. Replace must
	// emit rows in (AppID, InstanceID) order.
	r.Replace([]InstanceStat{
		{AppID: "zeta", InstanceID: "i-1", SampledAt: now},
		{AppID: "alpha", InstanceID: "i-9", SampledAt: now},
		{AppID: "alpha", InstanceID: "i-1", SampledAt: now},
		{AppID: "alpha", InstanceID: "i-2", SampledAt: now},
		{AppID: "zeta", InstanceID: "i-2", SampledAt: now},
	})
	got := r.SnapshotAll()
	want := []string{"alpha/i-1", "alpha/i-2", "alpha/i-9", "zeta/i-1", "zeta/i-2"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		gotKey := got[i].AppID + "/" + got[i].InstanceID
		if gotKey != want[i] {
			t.Errorf("SnapshotAll[%d] = %s, want %s", i, gotKey, want[i])
		}
	}
}

// TestReader_ReplaceAtomicVisibility is now covered as the
// PropertyReader_NoTornReads deterministic property test in
// reader_property_test.go — keeping a single property test there
// and a stable inventory in this file.

// TestReader_MaxInflightForApp pins the PR-B (issue #462)
// accessor across the three outcomes that matter to PR-C's
// scale-up trigger:
//
//   - app not in snapshot  → (0, false) — "no signal", caller
//     falls back to "do not scale" semantics.
//   - app present, all InflightRequests == 0 → (0, true) —
//     "app has live instances but they are idle", caller
//     treats as a valid zero reading.
//   - app present, max = 5 → (5, true) — load-bearing pin
//     the trigger compares against target.concurrent_requests.
func TestReader_MaxInflightForApp(t *testing.T) {
	now := time.Now()

	t.Run("AppNotInSnapshot", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app-other", InstanceID: "i-1", SampledAt: now, InflightRequests: 9},
		})
		got, ok := r.MaxInflightForApp("app-missing")
		if ok || got != 0 {
			t.Errorf("MaxInflightForApp(missing) = (%d, %v), want (0, false)", got, ok)
		}
	})

	t.Run("AppPresentAllIdle", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-1", SampledAt: now, InflightRequests: 0},
			{AppID: "app1", InstanceID: "i-2", SampledAt: now, InflightRequests: 0},
		})
		got, ok := r.MaxInflightForApp("app1")
		if !ok {
			t.Errorf("MaxInflightForApp(app1) ok=false, want true (app has live instances)")
		}
		if got != 0 {
			t.Errorf("MaxInflightForApp(app1) = %d, want 0 (all instances idle)", got)
		}
	})

	t.Run("AppPresentReturnsMax", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-1", SampledAt: now, InflightRequests: 2},
			{AppID: "app1", InstanceID: "i-2", SampledAt: now, InflightRequests: 5},
			{AppID: "app1", InstanceID: "i-3", SampledAt: now, InflightRequests: 1},
			// Different app must NOT contribute to the max.
			{AppID: "app-other", InstanceID: "i-9", SampledAt: now, InflightRequests: 99},
		})
		got, ok := r.MaxInflightForApp("app1")
		if !ok {
			t.Fatalf("MaxInflightForApp(app1) ok=false, want true")
		}
		if got != 5 {
			t.Errorf("MaxInflightForApp(app1) = %d, want 5 (max across i-1..i-3, ignoring app-other)", got)
		}
	})
}

// TestReader_MaxCPU pins the PR-C (issue #462) accessor across
// the three outcomes that matter to the scale-up trigger:
//
//   - app not in snapshot  → (0, false) — "no signal", caller
//     falls back to "do not scale" semantics.
//   - app present, all CPU=Unknown → (0, true) — "app has live
//     instances but no baseline yet", caller treats as a valid
//     zero reading (matches MaxInflightForApp semantics).
//   - app present, all CPU=Valid with max=85 → (85, true) —
//     load-bearing pin the trigger compares against
//     target.cpu_pct.
//   - app present, mixed CPU validity — only CPU=Valid rows
//     contribute; Unknown rows are dropped (mirror wire shape).
//   - app present, two apps — per-app filter excludes the
//     other app's rows.
func TestReader_MaxCPU(t *testing.T) {
	now := time.Now()

	t.Run("AppNotInSnapshot", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app-other", InstanceID: "i-1", SampledAt: now, CPUPct: 90, CPU: Valid},
		})
		got, ok := r.MaxCPU("app-missing")
		if ok || got != 0 {
			t.Errorf("MaxCPU(missing) = (%v, %v), want (0, false)", got, ok)
		}
	})

	t.Run("AppPresentAllUnknown", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-1", SampledAt: now, CPUPct: 0, CPU: Unknown},
			{AppID: "app1", InstanceID: "i-2", SampledAt: now, CPUPct: 0, CPU: Unknown},
		})
		got, ok := r.MaxCPU("app1")
		if !ok {
			t.Errorf("MaxCPU(app1) ok=false, want true (app has live instances)")
		}
		if got != 0 {
			t.Errorf("MaxCPU(app1) = %v, want 0 (all CPU=Unknown skipped)", got)
		}
	})

	t.Run("AppPresentReturnsMax", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-1", SampledAt: now, CPUPct: 25, CPU: Valid},
			{AppID: "app1", InstanceID: "i-2", SampledAt: now, CPUPct: 85, CPU: Valid},
			{AppID: "app1", InstanceID: "i-3", SampledAt: now, CPUPct: 50, CPU: Valid},
			// Different app must NOT contribute to the max.
			{AppID: "app-other", InstanceID: "i-9", SampledAt: now, CPUPct: 99, CPU: Valid},
		})
		got, ok := r.MaxCPU("app1")
		if !ok {
			t.Fatalf("MaxCPU(app1) ok=false, want true")
		}
		if got != 85 {
			t.Errorf("MaxCPU(app1) = %v, want 85 (max across i-1..i-3, ignoring app-other)", got)
		}
	})

	t.Run("AppPresentMixedValidity", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-1", SampledAt: now, CPUPct: 70, CPU: Valid},
			{AppID: "app1", InstanceID: "i-2", SampledAt: now, CPUPct: 0, CPU: Unknown},
			{AppID: "app1", InstanceID: "i-3", SampledAt: now, CPUPct: 40, CPU: Valid},
		})
		got, ok := r.MaxCPU("app1")
		if !ok {
			t.Fatalf("MaxCPU(app1) ok=false, want true (one valid row)")
		}
		if got != 70 {
			t.Errorf("MaxCPU(app1) = %v, want 70 (Unknown row skipped)", got)
		}
	})
}

func TestReader_SignalFreshness(t *testing.T) {
	stale := time.Now().Add(-DefaultFreshness - time.Second)
	fresh := time.Now()

	t.Run("staleInflightIsAbsent", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{{
			AppID: "app1", InstanceID: "i-1", SampledAt: stale,
			InflightRequests: 99,
		}})
		if got, ok := r.MaxInflightForApp("app1"); ok || got != 0 {
			t.Fatalf("MaxInflightForApp(stale) = (%d, %v), want (0, false)", got, ok)
		}
	})

	t.Run("staleCPUIsAbsent", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{{
			AppID: "app1", InstanceID: "i-1", SampledAt: stale,
			CPUPct: 99, CPU: Valid,
		}})
		if got, ok := r.MaxCPU("app1"); ok || got != 0 {
			t.Fatalf("MaxCPU(stale) = (%v, %v), want (0, false)", got, ok)
		}
	})

	t.Run("staleRowCannotWinAgainstFreshRow", func(t *testing.T) {
		r := NewReader()
		r.Replace([]InstanceStat{
			{AppID: "app1", InstanceID: "i-stale", SampledAt: stale, InflightRequests: 99, CPUPct: 99, CPU: Valid},
			{AppID: "app1", InstanceID: "i-fresh", SampledAt: fresh, InflightRequests: 3, CPUPct: 12, CPU: Valid},
		})
		if got, ok := r.MaxInflightForApp("app1"); !ok || got != 3 {
			t.Fatalf("MaxInflightForApp(mixed) = (%d, %v), want (3, true)", got, ok)
		}
		if got, ok := r.MaxCPU("app1"); !ok || got != 12 {
			t.Fatalf("MaxCPU(mixed) = (%v, %v), want (12, true)", got, ok)
		}
	})
}

func TestReader_RequestRatesPerSecondAt(t *testing.T) {
	r := NewReader()
	base := time.Unix(1_000_000, 0)
	r.Replace([]InstanceStat{{
		AppID: "app1", InstanceID: "i-1", SampledAt: base,
		RequestCountTotal: 10, RequestCountValid: true,
	}})
	r.Replace([]InstanceStat{{
		AppID: "app1", InstanceID: "i-1", SampledAt: base.Add(time.Second),
		RequestCountTotal: 40, RequestCountValid: true,
	}})
	rates := r.RequestRatesPerSecondAt(base.Add(time.Second))
	if got := rates["app1"]; got != 30 {
		t.Fatalf("app1 rate = %v, want 30", got)
	}
	rates["app1"] = 999
	if got := r.RequestRatesPerSecondAt(base.Add(time.Second))["app1"]; got != 30 {
		t.Fatalf("mutating returned map changed reader rate to %v", got)
	}
	if got := r.RequestRatesPerSecondAt(base.Add(time.Second + DefaultFreshness + time.Nanosecond)); len(got) != 0 {
		t.Fatalf("stale rates = %v, want empty", got)
	}
}
