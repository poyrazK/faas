package alerts_test

// adr: 20261104142830 — after a downgrade, only the oldest enabled rules
// within the plan's per-app and per-account caps evaluate; the rest are
// skipped as over_plan_limit, and disabling one lets the next run.

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestEvaluator_RulesPastThePlanCapStopEvaluating(t *testing.T) {
	store := state.NewMemStore()
	first, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	ids := []string{first.ID}
	for i, name := range []string{"second", "third"} {
		r := first
		r.ID, r.Name = "", name
		r.CreatedAt = first.CreatedAt.Add(time.Duration(i+1) * time.Second)
		created, err := store.CreateAlertRule(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.ID)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, &selectivePromQL{fn: func(string) (float64, error) { return 12, nil }}, ident, dispatch)
	perApp := 2
	ev.SetRuleLimits(func(context.Context, string) (int, int, bool) { return perApp, 10, true })

	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 2 || stats.SkippedOverPlanLimit != 1 {
		t.Fatalf("stats = %+v; want the two oldest rules fired and the newest skipped", stats)
	}
	newest, err := store.AlertRuleByID(context.Background(), ids[2])
	if err != nil || newest.State == state.AlertStateFiring {
		t.Fatalf("newest rule = %+v, %v; want it not evaluated", newest, err)
	}

	// Disabling an older rule makes room: the newest now evaluates.
	disabled := false
	if _, err := store.UpdateAlertRule(context.Background(), ids[0], state.UpdateAlertRuleParams{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	stats, err = ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.SkippedOverPlanLimit != 0 || stats.Fired != 1 {
		t.Fatalf("after disabling one = %+v; want the newest rule fired", stats)
	}
	var _ alerts.RuleLimits = func(context.Context, string) (int, int, bool) { return 0, 0, false }
}
