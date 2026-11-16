package apidserver_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProductionBucketFollowsTheServingLane pins that a "production" bucket on
// a project app served from the legacy default lane is created in default,
// the scope its bindings' secrets are delivered from.
func TestProductionBucketFollowsTheServingLane(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	if err := (*(*e.S()).RuntimeConfig()).Apply(apidserver.RuntimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	(*e.S()).WithObjectStorage(apidserver.ObjectRegistry(t, &apidserver.FakeObjectProvider{}, &apidserver.FakeObjectProvider{}, "external"))
	ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: api.DefaultEnvScope, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:abc", Status: state.DeployLive, TrafficPercent: 100}); err != nil {
		t.Fatal(err)
	}
	b := apidserver.BucketResponse(t, e.Do(t, "POST", "/v1/apps/shop-api/buckets", map[string]any{"name": "assets", "scope": "production"}, nil), 201)
	if b.Scope != api.DefaultEnvScope {
		t.Fatalf("bucket scope = %q, want %q (the lane production serves from)", b.Scope, api.DefaultEnvScope)
	}
}
