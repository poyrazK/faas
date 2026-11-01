package apidserver_test

// adr: 20261031130000, 20261101030512

import (
	"context"
	"fmt"
	"net/http"
	"sort"
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

// A production deploy disables nothing when it is uploaded: it adds what it
// declares and records its whole declared set, applied when the release
// serves all production traffic. A trigger it drops keeps delivering until
// then, so a deploy that never goes live loses no events.
func TestSourceRefManifestRecordsEventSubscriptionIntent(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := apidserver.WithEventSubscriptionReconcile(context.Background())
	apply := func(triggers ...gregalemanifest.EventTrigger) []string {
		t.Helper()
		manifest := &gregalemanifest.Manifest{EventTriggers: triggers}
		if err := manifest.Validate(); err != nil {
			t.Fatal(err)
		}
		staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true)
		if problem != nil {
			t.Fatalf("apply manifest: %s", problem.Detail)
		}
		return apidserver.SourceRefManifestEventIntent(staged)
	}
	created := gregalemanifest.EventTrigger{Source: "orders", Type: "order.created"}
	paid := gregalemanifest.EventTrigger{Source: "orders", Type: "order.paid"}

	apply(created, paid)
	first := enabledEventSubscriptions(t, store, app.ID)
	if len(first) != 2 {
		t.Fatalf("enabled subscriptions = %v, want order.created and order.paid", first)
	}

	intent := apply(paid)
	if got := enabledEventSubscriptions(t, store, app.ID); len(got) != 2 {
		t.Fatalf("after dropping order.created at upload: enabled = %v, want both still delivering", got)
	}
	if len(intent) != 1 || intent[0] != first["order.paid"] {
		t.Fatalf("recorded intent = %v, want only order.paid %s", intent, first["order.paid"])
	}

	if intent := apply(); intent == nil || len(intent) != 0 {
		t.Fatalf("a manifest without event_triggers records %v, want an empty declared set", intent)
	}

	// Without the production marker (stage deploys, batch create) nothing
	// is recorded.
	staged, problem := srv.ApplySourceRefManifest(context.Background(), acct, app, &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{paid}}, "", true)
	if problem != nil || apidserver.SourceRefManifestEventIntent(staged) != nil {
		t.Fatalf("non-production apply recorded %v, %v; want none", apidserver.SourceRefManifestEventIntent(staged), problem)
	}
}

// A manifest whose event_triggers name an app that does not exist (an app
// renamed since) would declare nothing for this app; it only adds.
func TestSourceRefManifestUnknownAppRecordsNoIntent(t *testing.T) {
	srv, _, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := apidserver.WithEventSubscriptionReconcile(context.Background())
	manifest := &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{{App: "renamed-away", Source: "orders", Type: "order.created"}}}
	staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, manifest, "", true)
	if problem != nil {
		t.Fatalf("apply manifest: %s", problem.Detail)
	}
	if intent := apidserver.SourceRefManifestEventIntent(staged); intent != nil {
		t.Fatalf("intent = %v, want none when the manifest names an unknown app", intent)
	}
}

func eventTriggers(n int, prefix string) []gregalemanifest.EventTrigger {
	triggers := make([]gregalemanifest.EventTrigger, 0, n)
	for i := 0; i < n; i++ {
		triggers = append(triggers, gregalemanifest.EventTrigger{Source: "orders", Type: fmt.Sprintf("%s.kind%02d", prefix, i)})
	}
	return triggers
}

// A production deploy is counted as its declared set will stand once live:
// replacing every trigger of an app at its cap is allowed, declaring one more
// than the cap is refused with the limit and the observed count.
func TestSourceRefManifestEnforcesEventSubscriptionLimit(t *testing.T) {
	srv, _, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := apidserver.WithEventSubscriptionReconcile(context.Background())
	limits := api.MustLimitsFor(acct.Plan)
	if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: eventTriggers(limits.EventSubscriptionsPerApp, "old")}, "", true); problem != nil {
		t.Fatalf("deploy at the cap: %s", problem.Detail)
	}
	if _, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: eventTriggers(limits.EventSubscriptionsPerApp, "new")}, "", true); problem != nil {
		t.Fatalf("replacing every trigger at the cap: %s", problem.Detail)
	}
	staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: eventTriggers(limits.EventSubscriptionsPerApp+1, "more")}, "", true)
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

// Applies outside a production deploy (batch create, stage deploys) count
// what is enabled, so they cannot grow an app past its cap.
func TestBatchEventTriggersCountTowardTheLimit(t *testing.T) {
	srv, store, acct, app := apidserver.SourceRefAsyncRouteTestFixture(t)
	limits := api.MustLimitsFor(acct.Plan)
	if _, problem := srv.ApplySourceRefManifest(context.Background(), acct, app, &gregalemanifest.Manifest{EventTriggers: eventTriggers(limits.EventSubscriptionsPerApp, "first")}, "", true); problem != nil {
		t.Fatalf("apply at the cap: %s", problem.Detail)
	}
	staged, problem := srv.ApplySourceRefManifest(context.Background(), acct, app, &gregalemanifest.Manifest{EventTriggers: eventTriggers(1, "extra")}, "", true)
	if problem == nil || problem.Code != api.CodePlanEventSubscriptionQuota {
		t.Fatalf("apply past the cap = %+v, want %s", problem, api.CodePlanEventSubscriptionQuota)
	}
	if err := srv.RollbackSourceRefManifest(context.Background(), staged); err != nil {
		t.Fatal(err)
	}
	got := enabledEventSubscriptions(t, store, app.ID)
	types := make([]string, 0, len(got))
	for typ := range got {
		types = append(types, typ)
	}
	sort.Strings(types)
	if len(types) != limits.EventSubscriptionsPerApp {
		t.Fatalf("after the refused apply enabled = %v, want the %d at the cap", types, limits.EventSubscriptionsPerApp)
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
