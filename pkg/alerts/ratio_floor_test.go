package alerts_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// adr: 20261109120000 — a share of a handful of requests is not evidence:
// one failed wake out of three requests must not page as a 33% error rate.
func TestEvaluator_RatioRuleNeedsEnoughRequests(t *testing.T) {
	cases := []struct {
		name     string
		requests float64
		wantFire bool
	}{
		{"three requests", 3, false},
		{"enough requests", 40, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident, _ := seedRuleForApp(t, store, "ratio-app", state.AlertMetricErrorRate, state.AlertGt, 5)
			prom := &selectivePromQL{fn: func(q string) (float64, error) {
				if strings.HasPrefix(q, "sum(increase(gateway_request_duration_seconds_count") {
					return tc.requests, nil
				}
				return 33, nil
			}}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev, _ := makeEvaluator(t, store, prom, ident, dispatch)
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got := stats.Fired > 0; got != tc.wantFire {
				t.Fatalf("fired = %v, want %v (stats %+v)", got, tc.wantFire, stats)
			}
			got, err := store.AlertRuleByID(context.Background(), rule.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.wantFire && got.State != state.AlertStateUnknown {
				t.Fatalf("state = %s, want unknown", got.State)
			}
		})
	}
}
