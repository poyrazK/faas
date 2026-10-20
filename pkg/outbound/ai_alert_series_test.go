package outbound

// adr: 1340 — counters that alert rules read by reason or outcome alone exist
// at zero from startup, so increase() sees a process's first canary rollback
// or unknown batch (ADR-1431).

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestAlertedAICountersStartAtZero(t *testing.T) {
	reg := prometheus.NewRegistry()
	if _, err := NewMetrics(reg); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		metric string
		series int
	}{
		{"outbound_ai_route_canary_rollbacks_total", 2},
		{"outbound_ai_batches_settled_total", 3},
	} {
		got, err := testutil.GatherAndCount(reg, tc.metric)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.series {
			t.Errorf("%s has %d series at startup, want %d", tc.metric, got, tc.series)
		}
	}
}
