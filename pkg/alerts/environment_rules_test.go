package alerts_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// seedStageRule creates a rule like seedRule but in environment envID.
func seedStageRule(t *testing.T, store *state.MemStore, envID string, metric state.AlertMetric, threshold float64) (state.AlertRule, *age.X25519Identity) {
	t.Helper()
	base, ident, _ := seedRule(t, store, metric, state.AlertGt, threshold)
	if err := store.DeleteAlertRule(context.Background(), base.ID); err != nil {
		t.Fatal(err)
	}
	base.ID, base.EnvironmentID = "", envID
	if metric == state.AlertMetricFailedInvocs {
		base.FailureSource = state.AlertFailureAny
	}
	rule, err := store.CreateAlertRule(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	return rule, ident
}

func stageEvaluator(store *state.MemStore, prom *selectivePromQL, ident *age.X25519Identity, dispatch *recordingDispatcher, now time.Time) *alerts.Evaluator {
	return alerts.NewEvaluator(alerts.EvaluatorOptions{
		Store: store, PromQL: prom, Audit: audit.New(store, discardLog(), nil, "meterd"),
		Identity: func() *age.X25519Identity { return ident }, Dispatcher: dispatch,
		Now: func() time.Time { return now }, Log: discardLog(),
	})
}

// adr: 20261104120000 — a stage's request rule reads the stage's own series,
// never the app-wide ones production rules read.
func TestEvaluatorStageRuleReadsEnvironmentSeries(t *testing.T) {
	store := state.NewMemStore()
	rule, ident := seedStageRule(t, store, "env-1", state.AlertMetricErrorRate, 5)
	var mu sync.Mutex
	var queries []string
	prom := &selectivePromQL{fn: func(q string) (float64, error) {
		mu.Lock()
		defer mu.Unlock()
		queries = append(queries, q)
		if strings.Contains(q, "request_duration_seconds_count") && !strings.Contains(q, "class") {
			return 1000, nil // request_count
		}
		return 50, nil
	}}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	stats, err := stageEvaluator(store, prom, ident, dispatch, time.Now()).RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 1 {
		t.Fatalf("stats = %+v, want the stage rule to fire", stats)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(queries) == 0 {
		t.Fatal("no queries")
	}
	for _, q := range queries {
		if !strings.Contains(q, `environment="env-1"`) || !strings.Contains(q, `app="`+rule.AppID+`"`) || strings.Contains(q, "gateway_request_duration_seconds") {
			t.Fatalf("query %q does not read the stage's own series", q)
		}
	}
}

// adr: 20261104120000 — a stage's failed-invocations rule never counts
// production's failures (the store counts the stage's own; see
// state.CountFailedInvocationsInEnvironmentSince's tests).
func TestEvaluatorStageRuleIgnoresProductionInvocations(t *testing.T) {
	store := state.NewMemStore()
	rule, ident := seedStageRule(t, store, "env-1", state.AlertMetricFailedInvocs, 1)
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		if _, err := store.EnqueueInvocation(context.Background(), state.Invocation{AccountID: rule.AccountID, AppID: rule.AppID,
			Source: state.InvocationCron, Path: "/run", State: state.InvocationFailed,
			DueAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Minute)}); err != nil {
			t.Fatal(err)
		}
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev := stageEvaluator(store, &selectivePromQL{fn: func(string) (float64, error) { return 0, nil }}, ident, dispatch, now)
	if stats, err := ev.RunOnce(context.Background()); err != nil || stats.Fired != 0 {
		t.Fatalf("stats = %+v, %v; production failures must not fire the stage rule", stats, err)
	}
}
