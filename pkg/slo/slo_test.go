package slo

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeProm struct {
	queries []string
	fn      func(q string) (float64, error)
}

func (f *fakeProm) QueryScalar(_ context.Context, q string) (float64, error) {
	f.queries = append(f.queries, q)
	return f.fn(q)
}

func TestQueries(t *testing.T) {
	at := time.Unix(1_760_000_000, 0)
	tests := []struct {
		def       state.SLO
		wantGood  string
		wantTotal string
	}{
		{state.SLO{AppID: "app-1", SLI: state.SLIAvailability},
			`sum(increase(gateway_requests_total{app="app-1",code=~"2.."}[1h] @ 1760000000)) or vector(0)`,
			`sum(increase(gateway_requests_total{app="app-1",code=~"2..|5.."}[1h] @ 1760000000)) or vector(0)`},
		{state.SLO{AppID: "app-1", SLI: state.SLILatency, LatencyThresholdMS: 250},
			`sum(increase(gateway_request_duration_seconds_bucket{app="app-1",le="0.25"}[1h] @ 1760000000)) or vector(0)`,
			`sum(increase(gateway_request_duration_seconds_count{app="app-1"}[1h] @ 1760000000)) or vector(0)`},
		{state.SLO{AppID: "app-1", SLI: state.SLILatency, LatencyThresholdMS: 10000},
			`le="10"`, `gateway_request_duration_seconds_count`},
	}
	for _, tt := range tests {
		good, total, err := queries(tt.def, "1h", at)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(good, tt.wantGood) || !strings.Contains(total, tt.wantTotal) {
			t.Errorf("%+v:\n good  %s\n total %s", tt.def, good, total)
		}
	}
	if _, _, err := queries(state.SLO{AppID: `x"}`, SLI: state.SLIAvailability}, "1h", at); err == nil {
		t.Error("an app id that could break the selector must be refused")
	}
}

func TestBudgetMath(t *testing.T) {
	// 99.9% objective, 10,000 requests: the budget is 10 failures.
	tests := []struct {
		good, total     int64
		remaining, burn float64
	}{
		{10000, 10000, 1, 0},
		{9995, 10000, 0.5, 0.5},
		{9990, 10000, 0, 1},
		{9980, 10000, -1, 2},
	}
	for _, tt := range tests {
		rem, ok := BudgetRemaining(tt.good, tt.total, 9990)
		burn, _ := BurnRate(tt.good, tt.total, 9990)
		if !ok || math.Abs(rem-tt.remaining) > 1e-9 || math.Abs(burn-tt.burn) > 1e-9 {
			t.Errorf("good %d/%d: remaining %v burn %v, want %v %v", tt.good, tt.total, rem, burn, tt.remaining, tt.burn)
		}
	}
	if _, ok := BudgetRemaining(0, 0, 9990); ok {
		t.Error("no traffic must report not-ok rather than a budget")
	}
}

func TestCountsClampsAndRounds(t *testing.T) {
	prom := &fakeProm{fn: func(q string) (float64, error) {
		if strings.Contains(q, `code=~"2.."`) && !strings.Contains(q, "5..") {
			return 100.6, nil // extrapolated good above total
		}
		return 100.2, nil
	}}
	good, total, err := Counts(context.Background(), prom, state.SLO{AppID: "a", SLI: state.SLIAvailability}, "1h", time.Time{})
	if err != nil || good != 100 || total != 100 {
		t.Fatalf("got %d/%d (%v), want 100/100", good, total, err)
	}
}

func TestRollupFillsMissingHoursOldestFirst(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 30, 0, 0, time.UTC)
	store := state.NewMemStore()
	def, err := store.CreateSLO(context.Background(), state.SLO{AppID: "app-1", Name: "up", SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 30}, api.MaxSLOsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	prom := &fakeProm{fn: func(q string) (float64, error) {
		if strings.Contains(q, "5..") {
			return 10, nil
		}
		return 9, nil
	}}
	r := &Rollup{Store: store, Prom: prom}
	// The SLO was created "now" (wall clock), after `now`: nothing to record.
	if stats, err := r.RunOnce(context.Background(), now); err != nil || stats.Recorded != 0 {
		t.Fatalf("future-created SLO: %+v %v", stats, err)
	}

	// A day later every completed hour since creation is recorded once.
	later := def.CreatedAt.Add(25 * time.Hour)
	stats, err := r.RunOnce(context.Background(), later)
	if err != nil {
		t.Fatal(err)
	}
	wantHours := int(later.Truncate(time.Hour).Sub(def.CreatedAt.Truncate(time.Hour)) / time.Hour)
	if stats.Recorded != wantHours {
		t.Fatalf("recorded %d hours, want %d", stats.Recorded, wantHours)
	}
	sum, _ := store.SumSLOHours(context.Background(), def.ID, time.Time{})
	if sum.Hours != wantHours || sum.Good != int64(9*wantHours) || sum.Total != int64(10*wantHours) {
		t.Fatalf("sum = %+v", sum)
	}
	// A second pass is idempotent.
	if stats, _ := r.RunOnce(context.Background(), later); stats.Recorded != 0 {
		t.Fatalf("second pass recorded %d hours", stats.Recorded)
	}
}

func TestRollupCapsBackfillAndRetriesFailures(t *testing.T) {
	store := state.NewMemStore()
	def, err := store.CreateSLO(context.Background(), state.SLO{AppID: "app-1", Name: "up", SLI: state.SLIAvailability, ObjectiveBP: 9990, WindowDays: 30}, api.MaxSLOsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	far := def.CreatedAt.Add(30 * 24 * time.Hour)
	failing := &fakeProm{fn: func(string) (float64, error) { return 0, errors.New("prometheus down") }}
	stats, err := (&Rollup{Store: store, Prom: failing}).RunOnce(context.Background(), far)
	if err != nil || stats.Failed != 1 || stats.Recorded != 0 {
		t.Fatalf("outage pass: %+v %v; want one failure and no rows", stats, err)
	}
	ok := &fakeProm{fn: func(string) (float64, error) { return 1, nil }}
	stats, _ = (&Rollup{Store: store, Prom: ok}).RunOnce(context.Background(), far)
	if stats.Recorded != MaxHoursPerSLOPerTick {
		t.Fatalf("recorded %d, want the per-tick cap %d", stats.Recorded, MaxHoursPerSLOPerTick)
	}
	hours, _ := store.SLOHourStarts(context.Background(), def.ID, time.Time{})
	horizon := far.Truncate(time.Hour).Add(-time.Duration(api.SLORollupBackfillHours) * time.Hour)
	if !hours[0].Equal(horizon) {
		t.Fatalf("first recorded hour %v, want the backfill horizon %v", hours[0], horizon)
	}
}
