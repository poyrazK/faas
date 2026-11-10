package edgerules

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 20261108110000 — amendment 2026-11-09: each backfill pass reports its
// result, the items it handled, whether the fleet can open sealed values and
// whether values are left as written.
func TestHeaderBackfillPassMetrics(t *testing.T) {
	m := NewMetrics(prometheus.NewRegistry(), "apid")
	now := time.Unix(1_800_000_000, 0)
	for _, tc := range []struct {
		name           string
		rep            HeaderSecretBackfillReport
		err            error
		result         string
		fleet, pending float64
	}{
		{"fleet not ready", HeaderSecretBackfillReport{FleetNotReady: true}, nil, "fleet_not_ready", 0, 1},
		{"sealed some, one failed", HeaderSecretBackfillReport{RulesSealed: 2, PoliciesSealed: 1, VersionsFailed: 1}, nil, "pending", 1, 1},
		{"error keeps fleet state", HeaderSecretBackfillReport{}, errors.New("db down"), "error", 1, 1},
		{"complete", HeaderSecretBackfillReport{VersionsSealed: 3}, nil, "complete", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := testutil.ToFloat64(m.headerBackfillPasses.WithLabelValues(tc.result))
			m.headerBackfillPass(tc.rep, tc.err, now)
			if got := testutil.ToFloat64(m.headerBackfillPasses.WithLabelValues(tc.result)) - before; got != 1 {
				t.Fatalf("passes{result=%q} rose by %v", tc.result, got)
			}
			if got := testutil.ToFloat64(m.headerBackfillFleet); got != tc.fleet {
				t.Fatalf("fleet_ready = %v, want %v", got, tc.fleet)
			}
			if got := testutil.ToFloat64(m.headerBackfillPending); got != tc.pending {
				t.Fatalf("pending = %v, want %v", got, tc.pending)
			}
			if got := testutil.ToFloat64(m.headerBackfillLastPass); got != float64(now.Unix()) {
				t.Fatalf("last pass = %v", got)
			}
		})
	}
	for _, want := range []struct {
		kind, outcome string
		n             float64
	}{{"rule", "sealed", 2}, {"environment_policy", "sealed", 1}, {"version", "failed", 1}, {"version", "sealed", 3}, {"rule", "failed", 0}} {
		if got := testutil.ToFloat64(m.headerBackfillValues.WithLabelValues(want.kind, want.outcome)); got != want.n {
			t.Fatalf("items{%s,%s} = %v, want %v", want.kind, want.outcome, got, want.n)
		}
	}
	var nilMetrics *Metrics
	nilMetrics.headerBackfillPass(HeaderSecretBackfillReport{}, nil, now) // no registry: records nothing
}
