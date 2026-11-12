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

// Section 6: branches and attached workloads share a project. A branch stays
// off its workloads' networks unless it joins them, and only a live branch
// may join; vmmd keeps the branch's instances off (or on) at every wake.
func TestEnvironmentBranchesAndPrivateNetworksShareAProject(t *testing.T) {
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
	branch := func(body string) (int, []byte) {
		req, rec := apidserver.ProjectRequest(http.MethodPost, "/v1/projects/shop/environments/production/branches", "shop", []byte(body))
		req.SetPathValue("environment", "production")
		srv.CreateProjectEnvironmentBranch(rec, req, acct)
		return rec.Code, rec.Body.Bytes()
	}
	*srv.EnvironmentBranchContainmentEnabled() = true

	if code := attach(); code != http.StatusAccepted {
		t.Fatalf("attach = %d", code)
	}
	code, body := branch(`{"slug":"pr-1","ttl":"2h"}`)
	var created api.ProjectEnvironmentResponse
	if code != http.StatusCreated || json.Unmarshal(body, &created) != nil || created.Branch == nil || created.Branch.PrivateNetworks != api.EnvironmentBranchPrivateNetworksOff {
		t.Fatalf("branch of an attached project = %d %s", code, body)
	}
	code, body = branch(`{"slug":"pr-2","ttl":"2h","private_networks":"join"}`)
	if code != http.StatusCreated || json.Unmarshal(body, &created) != nil || created.Branch.PrivateNetworks != api.EnvironmentBranchPrivateNetworksJoin {
		t.Fatalf("join branch = %d %s", code, body)
	}
	for name, request := range map[string]string{
		"contained join": `{"slug":"pr-3","ttl":"2h","egress":"contained","private_networks":"join"}`,
		"unknown mode":   `{"slug":"pr-4","ttl":"2h","private_networks":"bridge"}`,
	} {
		var problem api.Problem
		if code, body := branch(request); code != http.StatusBadRequest || json.Unmarshal(body, &problem) != nil || problem.Code != api.CodeValidation {
			t.Errorf("%s = %d %s", name, code, body)
		}
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-3"); err == nil {
		t.Fatal("the refused branch left an environment")
	}
	if rec := e.Do(t, http.MethodDelete, "/v1/apps/shop-web/network/private", nil, nil); rec.Code >= 300 {
		t.Fatalf("detach = %d %s", rec.Code, rec.Body.String())
	}
	if code := attach(); code != http.StatusAccepted {
		t.Fatalf("attach in a branched project = %d", code)
	}
	if _, err := store.GetAppPrivateNetworkAttachment(ctx, acct.ID, app.ID); err != nil {
		t.Fatalf("attachment in a branched project: %v", err)
	}
}
