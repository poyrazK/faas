package apidserver_test

// adr: 20261031130000

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

func enabledEventSubscriptions(t *testing.T, store interface {
	ListEventSubscriptionsForApp(context.Context, string) ([]state.EventSubscription, error)
}, appID string) map[string]string {
	t.Helper()
	subs, err := store.ListEventSubscriptionsForApp(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	enabled := map[string]string{}
	for _, sub := range subs {
		if sub.Enabled {
			enabled[sub.Type] = sub.ID
		}
	}
	return enabled
}

// A production deploy's gregale.yaml is the app's whole set of event
// subscriptions: a trigger it drops is disabled, a changed filter replaces the
// old subscription instead of adding a second one, a manifest without
// event_triggers disables them all, and declaring a trigger again re-enables
// the same subscription. A failed deploy restores what it changed.
func TestSourceRefManifestReconcilesEventSubscriptions(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := apidserver.WithEventSubscriptionReconcile(context.Background())
	apply := func(triggers ...gregalemanifest.EventTrigger) {
		t.Helper()
		manifest := &gregalemanifest.Manifest{EventTriggers: triggers}
		if err := manifest.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true); problem != nil {
			t.Fatalf("apply manifest: %s", problem.Detail)
		}
	}
	created := gregalemanifest.EventTrigger{Source: "orders", Type: "order.created"}
	paid := gregalemanifest.EventTrigger{Source: "orders", Type: "order.paid"}

	apply(created, paid)
	first := enabledEventSubscriptions(t, store, app.ID)
	if len(first) != 2 {
		t.Fatalf("enabled subscriptions = %v, want order.created and order.paid", first)
	}

	apply(paid)
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 1 || got["order.paid"] != first["order.paid"] {
		t.Fatalf("after dropping order.created: enabled = %v, want only the same order.paid", got)
	}

	apply()
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 0 {
		t.Fatalf("after a manifest without event_triggers: enabled = %v, want none", got)
	}

	apply(created)
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 1 || got["order.created"] != first["order.created"] {
		t.Fatalf("re-declared order.created: enabled = %v, want the original subscription %s", got, first["order.created"])
	}

	// A deploy that fails after reconciling restores the previous set.
	staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{paid}}, "", true)
	if problem != nil || !apidserver.SourceRefManifestNeedsRollback(staged) {
		t.Fatalf("swap apply = %+v, %v", staged, problem)
	}
	if err := srv.RollbackSourceRefManifest(ctx, staged); err != nil {
		t.Fatal(err)
	}
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 1 || got["order.created"] != first["order.created"] {
		t.Fatalf("after rollback: enabled = %v, want order.created restored alone", got)
	}
}

// A stage deploy, or a batch trigger create, only adds subscriptions.
func TestSourceRefManifestWithoutReconcileKeepsSubscriptions(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	for _, typ := range []string{"order.created", "order.paid"} {
		manifest := &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{{Source: "orders", Type: typ}}}
		if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true); problem != nil {
			t.Fatalf("apply manifest: %s", problem.Detail)
		}
	}
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 2 {
		t.Fatalf("enabled = %v, want both kept", got)
	}
}

// A deploy that would leave more enabled subscriptions than the plan allows
// for the app is refused with the limit and the observed count.
func TestSourceRefManifestEnforcesEventSubscriptionLimit(t *testing.T) {
	srv, _, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := apidserver.WithEventSubscriptionReconcile(context.Background())
	limits := api.MustLimitsFor(acct.Plan)
	triggers := make([]gregalemanifest.EventTrigger, 0, limits.EventSubscriptionsPerApp+1)
	for i := 0; i <= limits.EventSubscriptionsPerApp; i++ {
		triggers = append(triggers, gregalemanifest.EventTrigger{Source: "orders", Type: "order.kind" + string(rune('a'+i%26)) + string(rune('a'+i/26))})
	}
	manifest := &gregalemanifest.Manifest{EventTriggers: triggers}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem == nil || problem.Code != api.CodePlanEventSubscriptionQuota || problem.Status != http.StatusForbidden {
		t.Fatalf("over-limit deploy = %+v, want 403 %s", problem, api.CodePlanEventSubscriptionQuota)
	}
	if problem.Limit == nil || *problem.Limit != int64(limits.EventSubscriptionsPerApp) || problem.Observed == nil || *problem.Observed != int64(limits.EventSubscriptionsPerApp+1) {
		t.Fatalf("limit fields = %v/%v, want %d/%d", problem.Limit, problem.Observed, limits.EventSubscriptionsPerApp, limits.EventSubscriptionsPerApp+1)
	}
	if err := srv.RollbackSourceRefManifest(ctx, staged); err != nil {
		t.Fatal(err)
	}
}

// A stage deploy only adds subscriptions, so repeated stage deploys could
// pile them up past the plan's caps unless each one is counted. The deploy
// that crosses the cap is refused and rolls back.
func TestSourceRefManifestWithoutReconcileEnforcesEventSubscriptionLimit(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t, api.PlanHobby)
	ctx := context.Background()
	limit := api.MustLimitsFor(acct.Plan).EventSubscriptionsPerApp
	deploy := func(typ string) (*api.Problem, error) {
		manifest := &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{{Source: "orders", Type: typ}}}
		staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "staging", true)
		if problem != nil {
			return problem, srv.RollbackSourceRefManifest(ctx, staged)
		}
		return nil, nil
	}
	for i := range limit {
		if problem, _ := deploy(fmt.Sprintf("order.kind%d", i)); problem != nil {
			t.Fatalf("stage deploy %d: %s", i, problem.Detail)
		}
	}
	problem, err := deploy("order.over")
	if err != nil {
		t.Fatal(err)
	}
	if problem == nil || problem.Code != api.CodePlanEventSubscriptionQuota || problem.Status != http.StatusForbidden {
		t.Fatalf("stage deploy past the cap = %+v, want 403 %s", problem, api.CodePlanEventSubscriptionQuota)
	}
	if problem.Limit == nil || *problem.Limit != int64(limit) || problem.Observed == nil || *problem.Observed != int64(limit+1) {
		t.Fatalf("limit fields = %v/%v, want %d/%d", problem.Limit, problem.Observed, limit, limit+1)
	}
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != limit {
		t.Fatalf("enabled = %v, want the %d within the cap", got, limit)
	}
}
