package alerts_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// seedCustomMetricRule replaces seedRule's rule with a custom_metric rule
// naming metric, so the evaluator sees exactly one rule.
func seedCustomMetricRule(t *testing.T, store *state.MemStore, metric string, threshold float64) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, state.AlertMetricCustomMetric, state.AlertGt, threshold)
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatalf("DeleteAlertRule: %v", err)
	}
	base.ID, base.Name, base.CustomMetricName = "", base.Name+"-custom", metric
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}
	return rule, ident
}

func TestEvaluator_CustomMetric(t *testing.T) {
	cases := []struct {
		name      string
		kind      string // "" = metric never pushed
		value     float64
		wantFn    string
		wantFired int
		wantState state.AlertState
	}{
		{name: "gauge above threshold fires", kind: state.CustomMetricKindGauge, value: 150, wantFn: "avg_over_time(", wantFired: 1, wantState: state.AlertStateFiring},
		{name: "counter judged on rate", kind: state.CustomMetricKindCounter, value: 150, wantFn: "rate(", wantFired: 1, wantState: state.AlertStateFiring},
		{name: "no samples in window is unknown", kind: state.CustomMetricKindGauge, value: -1, wantState: state.AlertStateUnknown},
		{name: "deleted metric is unknown", kind: "", value: 150, wantState: state.AlertStateUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, ident := seedCustomMetricRule(t, store, "orders_pending", 100)
			if tc.kind != "" {
				if err := store.PutCustomMetricOfKind(context.Background(), rule.AppID, "orders_pending", tc.kind, 1, time.Now(), 5); err != nil {
					t.Fatalf("PutCustomMetricOfKind: %v", err)
				}
			}
			var queries []string
			prom := &selectivePromQL{fn: func(q string) (float64, error) {
				queries = append(queries, q)
				return tc.value, nil
			}}
			dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
			ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
				Store:      store,
				PromQL:     prom,
				Audit:      audit.New(store, discardLog(), nil, "meterd"),
				Identity:   func() *age.X25519Identity { return ident },
				Dispatcher: dispatch,
				Now:        func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) },
				Log:        discardLog(),
			})
			stats, err := ev.RunOnce(context.Background())
			if err != nil {
				t.Fatalf("RunOnce: %v", err)
			}
			if stats.Fired != tc.wantFired {
				t.Fatalf("stats = %+v; want Fired=%d", stats, tc.wantFired)
			}
			if tc.wantFn != "" && (len(queries) != 1 || !strings.Contains(queries[0], tc.wantFn) || !strings.Contains(queries[0], `name="orders_pending"`)) {
				t.Fatalf("queries = %q; want one %s query for orders_pending", queries, tc.wantFn)
			}
			got, err := store.AlertRuleByID(context.Background(), rule.ID)
			if err != nil {
				t.Fatalf("AlertRuleByID: %v", err)
			}
			if got.State != tc.wantState {
				t.Fatalf("state = %q; want %q", got.State, tc.wantState)
			}
		})
	}
}
