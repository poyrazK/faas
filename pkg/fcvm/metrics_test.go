package fcvm

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestNewDashboardGaugesRegistersAllFour asserts all four spec §12
// metric names are registered and exposed in the registry's text
// output. Catches a future edit that drops one (e.g. a rename) without
// touching the dashboard.
func TestNewDashboardGaugesRegistersAllFour(t *testing.T) {
	g := NewDashboardGauges(DashboardMetrics{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	g.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	wants := []string{
		"fcvm_snapshot_fleet_avg_bytes",
		"fcvm_snapshot_fleet_p95_bytes",
		"fcvm_resident_ram_pct",
		"fcvm_lv_fc_used_pct",
	}
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("metric %q not in registry output", w)
		}
	}
}

// TestDashboardGaugesFleetAvgAndP95 drives the snapshot list callback
// and asserts both average and p95 are computed correctly. Uses
// 20 snapshots with values 1..20 MiB to verify the nearest-rank p95
// (idx ceil(0.95*20)=19, 1-indexed → the 19th element = 19 MiB).
func TestDashboardGaugesFleetAvgAndP95(t *testing.T) {
	stats := make([]SnapshotStat, 20)
	for i := range stats {
		stats[i] = SnapshotStat{
			MemBytes:     int64(i+1) * 1024 * 1024,
			VMStateBytes: 0,
			DiskBytes:    0,
		}
	}
	g := NewDashboardGauges(DashboardMetrics{
		ListSnapshotStats: func(context.Context) ([]SnapshotStat, error) {
			return stats, nil
		},
	}).WithTTL(time.Hour)

	avg := g.avgFleet()
	wantAvg := float64(1+20) / 2.0 * 1024 * 1024 // (n+1)/2 for 1..n
	if avg != wantAvg {
		t.Errorf("avg = %v, want %v", avg, wantAvg)
	}

	p95 := g.p95Fleet()
	wantP95 := 19.0 * 1024 * 1024 // 19th element of 1..20, nearest-rank
	if p95 != wantP95 {
		t.Errorf("p95 = %v, want %v", p95, wantP95)
	}
}

// TestDashboardGaugesResidentPct drives the resident-bytes callback
// and asserts the percentage against api.RAMAdmissionCeilingMB.
func TestDashboardGaugesResidentPct(t *testing.T) {
	// 50% of ceiling.
	halfBytes := int64(0.5 * 47600 * 1024 * 1024)
	g := NewDashboardGauges(DashboardMetrics{
		ResidentBytes: func(context.Context) (int64, error) { return halfBytes, nil },
	}).WithTTL(time.Hour)

	if got := g.residentPct(); got != 50.0 {
		t.Errorf("resident_pct = %v, want 50.0", got)
	}
}

// TestDashboardGaugesSourceErrorKeepsPriorValue asserts the
// graceful-degradation contract: a transient source error must not
// reset the cached value to zero. Seed with 42, return an error on
// the next call, assert the gauge still reports 42.
func TestDashboardGaugesSourceErrorKeepsPriorValue(t *testing.T) {
	calls := 0
	g := NewDashboardGauges(DashboardMetrics{
		ResidentBytes: func(context.Context) (int64, error) {
			calls++
			if calls == 1 {
				return int64(0.42 * 47600 * 1024 * 1024), nil // 42%
			}
			return 0, errors.New("transient PG hiccup")
		},
	}).WithTTL(time.Hour)

	// First call seeds the cache.
	if got := g.residentPct(); got != 42.0 {
		t.Errorf("first call: resident_pct = %v, want 42.0", got)
	}
	// Force a refresh by zeroing the TTL.
	g.WithTTL(0)
	// Second call: source errors, but cache must keep 42.
	if got := g.residentPct(); got != 42.0 {
		t.Errorf("after error: resident_pct = %v, want 42.0 (graceful degradation)", got)
	}
}

// TestDashboardGaugesCacheTTSuppressesRefreshes asserts the
// high-frequency scrape path doesn't multiply the source calls. We
// call the gauge 100 times in a tight loop and verify the source was
// hit at most twice (once on first call, possibly once on the second
// only if the TTL boundary was crossed — but WithTTL(1h) keeps it
// strictly once).
func TestDashboardGaugesCacheTTSuppressesRefreshes(t *testing.T) {
	calls := 0
	g := NewDashboardGauges(DashboardMetrics{
		LvFcUsedPct: func(context.Context) (float64, error) {
			calls++
			return 42.0, nil
		},
	}).WithTTL(time.Hour)

	for i := 0; i < 100; i++ {
		_ = g.lvPct()
	}
	if calls != 1 {
		t.Errorf("source called %d times, want 1 (cache TTL suppressed the rest)", calls)
	}
}

// TestDefaultLvFcUsedPctEmptyName guards the bad-input path. The
// closure must return a non-nil error, not panic.
func TestDefaultLvFcUsedPctEmptyName(t *testing.T) {
	_, err := DefaultLvFcUsedPct("")(context.Background())
	if err == nil {
		t.Error("empty lv name: expected error, got nil")
	}
}

// TestColdBootMetricsRegistersAndIncrements asserts the counter is
// exposed under the spec §12 / ADR-016 name, and ObserveFallback on a
// non-nil receiver advances the counter (a future edit that breaks
// the pointer receiver would zero the counter on each call).
func TestColdBootMetricsRegistersAndIncrements(t *testing.T) {
	cbm := NewColdBootMetrics()
	cbm.ObserveFallback()
	cbm.ObserveFallback()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	cbm.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()
	if !strings.Contains(body, "vmmd_cold_boot_fallback_total 2") {
		t.Errorf("expected counter line in output, got:\n%s", body)
	}
}

// TestColdBootMetricsNilSafe is the contract that lets unit tests
// construct a Manager without wiring metrics. If a future edit makes
// the receiver dereference unconditionally, this panics.
func TestColdBootMetricsNilSafe(t *testing.T) {
	var cbm *ColdBootMetrics // nil
	cbm.ObserveFallback()    // must not panic
}
