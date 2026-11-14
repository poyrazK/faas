package apidserver_test

// adr: 20261104120000

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A stage deploy installs its event triggers into the stage, beside
// production's, records no production intent, and refuses work policies.
func TestSourceRefManifestInstallsEventTriggersInTheirEnvironment(t *testing.T) {
	srv, store, acct, _ := apidserver.SourceRefAsyncRouteTestFixture(t)
	ctx := context.Background()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: acct.ID, ProjectID: project.ID, Slug: "staging"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	created := gregalemanifest.EventTrigger{Source: "orders", Type: "order.created"}
	apply := func(ctx context.Context, scope string, triggers ...gregalemanifest.EventTrigger) []string {
		t.Helper()
		staged, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: triggers}, scope, true)
		if problem != nil {
			t.Fatalf("apply into %q: %s", scope, problem.Detail)
		}
		return apidserver.SourceRefManifestEventIntent(staged)
	}

	if intent := apply(apidserver.WithEventSubscriptionReconcile(ctx), "staging", created); intent != nil {
		t.Fatalf("a stage deploy recorded production intent %v", intent)
	}
	apply(apidserver.WithEventSubscriptionReconcile(ctx), "", created)
	subs, err := store.ListEventSubscriptionsForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	scopes := map[string]state.EventSubscription{}
	for _, sub := range subs {
		scopes[sub.DeploymentScope] = sub
	}
	if len(subs) != 2 || scopes["staging"].EnvironmentID != staging.ID || !scopes["staging"].Enabled ||
		scopes[""].EnvironmentID != "" || !scopes[""].Enabled {
		t.Fatalf("subscriptions = %+v, want one enabled per environment", subs)
	}

	// Redeploying the stage keeps its own row.
	apply(ctx, "staging", created)
	if again, _ := store.ListEventSubscriptionsForApp(ctx, app.ID); len(again) != 2 {
		t.Fatalf("redeploying the stage changed the subscriptions: %+v", again)
	}

	withPolicy := created
	withPolicy.WorkPolicy, withPolicy.WorkKey = "orders", "data.order_id"
	_, problem := srv.ApplySourceRefManifest(ctx, acct, app, &gregalemanifest.Manifest{EventTriggers: []gregalemanifest.EventTrigger{withPolicy}}, "staging", true)
	if problem == nil || problem.Status != http.StatusUnprocessableEntity {
		t.Fatalf("stage work policy = %+v, want 422", problem)
	}
}
