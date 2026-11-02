// adr: 20261031193635
package apidserver_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 4: a private-network attachment reaches every instance of its app,
// a branch's too, so a project has either environment branches or attached
// workloads. Each side refuses the other with the same problem.
func TestEnvironmentBranchesAndPrivateNetworksDoNotShareAProject(t *testing.T) {
	withPrivateNetworkEnabled(t, true)
	e := apidserver.Setup(t, api.PlanScale)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	if err := (*srv.RuntimeConfig()).Apply(apidserver.RuntimeConfigEnvironmentBranches, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main", ScanSource: state.ProjectScanSourceCompose})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: project.ID, Slug: "shop-web", WorkloadName: "web", Type: state.AppTypeApp, Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	parent, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage, ImageDigest: "sha256:web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, parent.ID, "/web.ext4", "layers/web.ext4", 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	attach := func() int {
		return e.Do(t, http.MethodPut, "/v1/apps/shop-web/network/private", privateNetworkRequest("prod-vpc", "fra1", []string{"10.20.0.0/16"}), nil).Code
	}
	branch := func(slug string) (int, api.Problem) {
		req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/branches", "shop", []byte(`{"slug":"`+slug+`","ttl":"2h"}`))
		req.SetPathValue("environment", "production")
		srv.CreateProjectEnvironmentBranch(rec, req, acct)
		var problem api.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &problem)
		return rec.Code, problem
	}

	if code := attach(); code != http.StatusAccepted {
		t.Fatalf("attach = %d", code)
	}
	if code, problem := branch("pr-1"); code != http.StatusConflict || problem.Code != api.CodeEnvironmentBranchPrivateNetwork {
		t.Fatalf("branch of an attached project = %d %+v", code, problem)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-1"); err == nil {
		t.Fatal("the refused branch left an environment")
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/shop-web/network/private", nil, nil); rec.Code >= 300 {
		t.Fatalf("detach = %d %s", rec.Code, rec.Body.String())
	}
	if code, problem := branch("pr-2"); code != http.StatusCreated {
		t.Fatalf("branch after detaching = %d %+v", code, problem)
	}
	rec := e.Do(t, http.MethodPut, "/v1/apps/shop-web/network/private", privateNetworkRequest("prod-vpc", "fra1", []string{"10.20.0.0/16"}), nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeEnvironmentBranchPrivateNetwork {
		t.Fatalf("attach in a branched project = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := store.GetAppPrivateNetworkAttachment(ctx, acct.ID, app.ID); err == nil {
		t.Fatal("the refused attachment was saved")
	}
}
