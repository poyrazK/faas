package alerts_test

import (
	"context"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// seedSyntheticRule creates a check on seedRule's app and replaces the
// seeded rule with one on metric naming that check.
func seedSyntheticRule(t *testing.T, store *state.MemStore, metric state.AlertMetric, cmp state.AlertComparison, threshold float64) (state.AlertRule, state.SyntheticCheck, *age.X25519Identity) {
	t.Helper()
	ctx := context.Background()
	base, ident, _ := seedRule(t, store, metric, cmp, threshold)
	check, err := store.CreateSyntheticCheck(ctx, state.SyntheticCheck{AccountID: base.AccountID, AppID: base.AppID, Name: "health", Method: "GET", Path: "/", TimeoutMS: 5000, IntervalSeconds: 300}, api.MaxSyntheticChecksPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAlertRule(ctx, base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.Name, base.SyntheticCheckID = "", base.Name+"-synthetic", check.ID
	base.WindowSpec = state.AlertWindowSpec("1h")
	rule, err := store.CreateAlertRule(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	return rule, check, ident
}

func evaluateSynthetic(t *testing.T, store *state.MemStore, ident *age.X25519Identity, now time.Time) alerts.Stats {
	t.Helper()
	ev := alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store:      store,
		Audit:      audit.New(store, discardLog(), nil, "meterd"),
		Identity:   func() *age.X25519Identity { return ident },
		Dispatcher: &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}},
		Now:        func() time.Time { return now },
		Log:        discardLog(),
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

func recordRuns(t *testing.T, store *state.MemStore, checkID string, now time.Time, outcomes ...bool) {
	t.Helper()
	// outcomes are newest first, five minutes apart.
	for i, ok := range outcomes {
		run := state.SyntheticCheckRun{CheckID: checkID, StartedAt: now.Add(-time.Duration(i) * 5 * time.Minute), OK: ok, StatusCode: 200, LatencyMS: 100 * (i + 1)}
		if !ok {
			run.StatusCode, run.ErrorClass = 503, "status"
		}
		if err := store.RecordSyntheticCheckRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEvaluator_SyntheticConsecutiveFailures(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	tests := []struct {
		name      string
		outcomes  []bool // newest first
		wantFired int
		wantState state.AlertState
	}{
		{"single blip does not fire", []bool{false, true, true}, 0, state.AlertStateOk},
		{"two in a row fires", []bool{false, false, true}, 1, state.AlertStateFiring},
		{"recovered does not fire", []bool{true, false, false}, 0, state.AlertStateOk},
		{"no runs is unknown", nil, 0, state.AlertStateUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := state.NewMemStore()
			rule, check, ident := seedSyntheticRule(t, store, state.AlertMetricSyntheticConsecutiveFailures, state.AlertGte, 2)
			recordRuns(t, store, check.ID, now, tt.outcomes...)
			if stats := evaluateSynthetic(t, store, ident, now); stats.Fired != tt.wantFired {
				t.Fatalf("stats = %+v; want Fired=%d", stats, tt.wantFired)
			}
			if got, _ := store.AlertRuleByID(context.Background(), rule.ID); got.State != tt.wantState {
				t.Fatalf("state = %q; want %q", got.State, tt.wantState)
			}
		})
	}
}

func TestEvaluator_SyntheticLatencyP95(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	store := state.NewMemStore()
	_, check, ident := seedSyntheticRule(t, store, state.AlertMetricSyntheticLatencyP95, state.AlertGt, 250)
	// Successful runs at 100, 200 and 300 ms within the hour: p95 = 300.
	recordRuns(t, store, check.ID, now, true, true, true)
	if stats := evaluateSynthetic(t, store, ident, now); stats.Fired != 1 {
		t.Fatalf("stats = %+v; want one fire", stats)
	}
}
