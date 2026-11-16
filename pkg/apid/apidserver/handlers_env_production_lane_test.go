package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestProductionValueWritesFollowTheServingLane pins that a project app
// served from the legacy default lane receives values written to
// "production": the write lands in the scope its live deployment reads, not
// in a "production" scope nothing serves.
func TestProductionValueWritesFollowTheServingLane(t *testing.T) {
	for _, tc := range []struct {
		name, deploymentScope, want string
	}{
		{name: "default lane", deploymentScope: api.DefaultEnvScope, want: api.DefaultEnvScope},
		{name: "production lane", deploymentScope: "production", want: "production"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := apidserver.Setup(t, api.PlanPro)
			ctx, store, acct := context.Background(), (*e.Store()), (*e.Acct())
			project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
			if err != nil {
				t.Fatal(err)
			}
			app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-api", WorkloadName: "api", Type: state.AppTypeApp, Status: state.AppActive})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: tc.deploymentScope, Kind: state.DeploymentKindImage,
				ImageDigest: "sha256:abc", Status: state.DeployLive, TrafficPercent: 100}); err != nil {
				t.Fatal(err)
			}
			if response := e.Do(t, "PUT", "/v1/apps/"+app.Slug+"/env/MODE?scope=production", api.PutAppEnvRequest{Value: "on"}, nil); response.Code != 200 {
				t.Fatalf("set production variable: %d %s", response.Code, response.Body.String())
			}
			rows, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, tc.want)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 || rows[0].Key != "MODE" {
				t.Fatalf("scope %q holds %+v, want MODE", tc.want, rows)
			}
		})
	}
}
