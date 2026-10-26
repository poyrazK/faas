package state_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemSourceSpoolInFlightPaths(t *testing.T) {
	testSourceSpoolInFlightPaths(t, state.NewMemStore())
}

// The spool sweeper keeps every file a deployment that can still build or
// image names (ADR-20261026120000): its source archive and its build log.
// Terminal deployments release theirs. It returns the app and its
// deployment IDs keyed by status name.
func testSourceSpoolInFlightPaths(t *testing.T, store state.ContractTestStore) (state.App, map[string]string) {
	t.Helper()
	ctx := t.Context()
	s, ok := any(store).(state.SourceSpoolStore)
	if !ok {
		t.Fatalf("%T has no source spool listing", store)
	}
	acct, err := store.CreateAccount(ctx, "spool-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "spool-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/spool/" + app.ID + "/"
	ids := map[string]string{}
	for _, tc := range []struct {
		name   string
		status state.DeploymentStatus
	}{{"pending", state.DeployPending}, {"imaging", state.DeployImaging}, {"live", state.DeployLive}, {"failed", state.DeployFailed}} {
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending, Scope: tc.name,
			SourcePath: prefix + tc.name + ".tar.gz", LogPath: prefix + tc.name + "/build.log"})
		if err != nil {
			t.Fatal(err)
		}
		ids[tc.name] = d.ID
		if tc.status != state.DeployPending {
			if err := store.UpdateDeploymentStatus(ctx, d.ID, tc.status, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	paths, err := s.ListInFlightDeploymentSpoolPaths(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mine []string
	for _, p := range paths {
		if strings.HasPrefix(p, prefix) {
			mine = append(mine, strings.TrimPrefix(p, prefix))
		}
	}
	sort.Strings(mine)
	if want := "imaging.tar.gz,imaging/build.log,pending.tar.gz,pending/build.log"; strings.Join(mine, ",") != want {
		t.Fatalf("in-flight paths = %v, want %s", mine, want)
	}
	return app, ids
}
