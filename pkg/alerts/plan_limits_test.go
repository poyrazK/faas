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
	ev.SetRuleLimits(func(_ context.Context, r state.AlertRule) (alerts.RuleLimit, bool) {
		return alerts.RuleLimit{PerApp: perApp, PerAccount: 10, Subject: r.AccountID}, true
	})

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
}

// adr: 20261109120000 — each rule gets its owner's caps: a rule on an app an
// organization owns follows the organization's plan, not the creating
// member's, and a rule whose owner cannot be read keeps evaluating.
func TestEvaluator_PlanCapsFollowEachRulesOwner(t *testing.T) {
	store := state.NewMemStore()
	first, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	unreadable := ""
	for i, name := range []string{"second", "third", "fourth"} {
		r := first
		r.ID, r.Name = "", name
		r.CreatedAt = first.CreatedAt.Add(time.Duration(i+1) * time.Second)
		created, err := store.CreateAlertRule(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		unreadable = created.ID
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, &selectivePromQL{fn: func(string) (float64, error) { return 12, nil }}, ident, dispatch)
	// The organization's plan allows three rules per app (a member's own
	// plan might allow one); the newest rule's owner cannot be read.
	ev.SetRuleLimits(func(_ context.Context, r state.AlertRule) (alerts.RuleLimit, bool) {
		if r.ID == unreadable {
			return alerts.RuleLimit{}, false
		}
		return alerts.RuleLimit{PerApp: 3, PerAccount: 10, Subject: "org"}, true
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 4 || stats.SkippedOverPlanLimit != 0 {
		t.Fatalf("stats = %+v; want every rule fired under the organization's caps", stats)
	}
}

func TestEvaluator_PerAccountCapCountsPerBillingSubject(t *testing.T) {
	store := state.NewMemStore()
	first, ident, _ := seedRule(t, store, state.AlertMetricErrorRate, state.AlertGt, 5)
	second := first
	second.ID, second.Name, second.CreatedAt = "", "second", first.CreatedAt.Add(time.Second)
	created, err := store.CreateAlertRule(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := &recordingDispatcher{result: webhookout.Result{StatusCode: 200, Attempts: 1}}
	ev, _ := makeEvaluator(t, store, &selectivePromQL{fn: func(string) (float64, error) { return 12, nil }}, ident, dispatch)
	// One rule per subject, and each rule has its own subject: neither is over.
	ev.SetRuleLimits(func(_ context.Context, r state.AlertRule) (alerts.RuleLimit, bool) {
		subject := "personal"
		if r.ID == created.ID {
			subject = "org"
		}
		return alerts.RuleLimit{PerApp: 10, PerAccount: 1, Subject: subject}, true
	})
	stats, err := ev.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Fired != 2 || stats.SkippedOverPlanLimit != 0 {
		t.Fatalf("stats = %+v; want one rule per subject fired", stats)
	}
}
