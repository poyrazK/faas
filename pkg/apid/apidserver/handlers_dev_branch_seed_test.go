// adr: 20261031193635
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 6b: a rehearsal member is seeded from a simulated branch's workload,
// reusing its build. A live branch cannot seed one.
func TestSeedDevSessionFromSimulatedEnvironmentBranch(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store, acct := *e.Store(), *e.Acct()
	ctx := t.Context()
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web",
		Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:shop"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/shop.ext4", "layers/shop.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	for slug, mode := range map[string]string{"sim-1": state.EnvironmentBranchEgressSimulated, "pr-1": state.EnvironmentBranchEgressLive} {
		if _, _, _, err := store.CreateProjectEnvironmentBranch(ctx, state.ProjectEnvironmentBranchCreate{
			AccountID: acct.ID, ProjectID: project.ID, ParentSlug: "production", Slug: slug,
			ExpiresAt: time.Now().Add(time.Hour), EgressMode: mode,
			MaxDepth: api.EnvironmentBranchMaxDepth, MaxBranches: limits.EnvironmentBranches,
		}, limits); err != nil {
			t.Fatal(err)
		}
		d, err := store.CreateDeploymentForEnvironmentBranch(ctx, acct.ID, project.ID, slug, app.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	const run = "abababababababababababababababab"
	r := e.Do(t, "PUT", "/v1/dev/sessions/shop-web", api.UpsertDevSessionRequest{WorkspaceID: run}, nil)
	if r.Code != http.StatusCreated {
		t.Fatalf("session: %d %s", r.Code, r.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(r.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	seed := api.SeedDevSessionFromEnvironmentBranchRequest{WorkspaceID: run, Project: "shop", Environment: "sim-1", Workload: "web"}

	if r := e.Do(t, "POST", "/v1/dev/sessions/shop-web/seed", seed, nil); r.Code != http.StatusConflict {
		t.Fatalf("seed with the gates off: %d %s", r.Code, r.Body.String())
	}
	s := *e.S()
	s.EnableEnvironmentBranchesForTest(t)
	*s.EnvironmentBranchContainmentEnabled() = true

	live := seed
	live.Environment = "pr-1"
	if r := e.Do(t, "POST", "/v1/dev/sessions/shop-web/seed", live, nil); r.Code != http.StatusConflict {
		t.Fatalf("seed from a live branch: %d %s", r.Code, r.Body.String())
	}
	missing := seed
	missing.Workload = "api"
	if r := e.Do(t, "POST", "/v1/dev/sessions/shop-web/seed", missing, nil); r.Code != http.StatusNotFound {
		t.Fatalf("seed of an unknown workload: %d %s", r.Code, r.Body.String())
	}
	r = e.Do(t, "POST", "/v1/dev/sessions/shop-web/seed", seed, nil)
	if r.Code != http.StatusAccepted {
		t.Fatalf("seed: %d %s", r.Code, r.Body.String())
	}
	var seeded api.SeedDevSessionFromEnvironmentBranchResponse
	if err := json.Unmarshal(r.Body.Bytes(), &seeded); err != nil {
		t.Fatal(err)
	}
	if seeded.AppSlug != session.App.Slug || seeded.Status != string(state.DeploySnapshotting) || seeded.DeploymentID == "" {
		t.Fatalf("seed response = %+v", seeded)
	}
	d, err := store.DeploymentByID(ctx, seeded.DeploymentID)
	if err != nil || d.AppID != session.App.ID || d.RootfsKey != "layers/shop.ext4" {
		t.Fatalf("seeded deployment = %+v, %v", d, err)
	}
}
