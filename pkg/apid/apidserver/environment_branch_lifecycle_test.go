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

// ADR-20261031193635 branch lifecycle (amended 2026-11-12): a branch reports
// its state, its expiry moves within its longest lifetime, and a reset makes
// it again from its parent with its settings and new workload URLs.
func TestEnvironmentBranchStatusExtendAndReset(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv, store, acct := *e.S(), *e.Store(), *e.Acct()
	ctx := t.Context()
	if err := (*srv.RuntimeConfig()).Apply(apidserver.RuntimeConfigEnvironmentBranches, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "shop", ProductionBranch: "main"})
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
	decode := func(t *testing.T, code int, body []byte, want int) api.ProjectEnvironmentResponse {
		t.Helper()
		if code != want {
			t.Fatalf("status = %d %s; want %d", code, body, want)
		}
		var out api.ProjectEnvironmentResponse
		if err := json.Unmarshal(body, &out); err != nil || out.Branch == nil {
			t.Fatalf("response = %s, %v; want a branch", body, err)
		}
		return out
	}
	base := "/v1/projects/shop/environments/"
	rec := e.Do(t, http.MethodPost, base+"production/branches", api.CreateProjectEnvironmentBranchRequest{Slug: "pr-1", TTL: "2h"}, nil)
	created := decode(t, rec.Code, rec.Body.Bytes(), http.StatusCreated)

	rec = e.Do(t, http.MethodGet, base+"pr-1/branch", nil, nil)
	status := decode(t, rec.Code, rec.Body.Bytes(), http.StatusOK)
	if status.ID != created.ID || status.Branch.Parent != "production" || status.Branch.CreatedAt == "" || len(status.Branch.Workloads) != 1 ||
		status.Branch.Workloads[0].URL != created.Branch.Workloads[0].URL || status.Branch.Workloads[0].Workload != "web" {
		t.Fatalf("status = %+v; want the branch with its one workload at %s", status.Branch, created.Branch.Workloads[0].URL)
	}
	if rec := e.Do(t, http.MethodGet, base+"production/branch", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("production's branch view = %d %s; want 404", rec.Code, rec.Body)
	}

	rec = e.Do(t, http.MethodPost, base+"pr-1/branch/extend", api.ExtendProjectEnvironmentBranchRequest{TTL: "48h"}, nil)
	extended := decode(t, rec.Code, rec.Body.Bytes(), http.StatusOK)
	expires, err := time.Parse(time.RFC3339Nano, extended.Branch.ExpiresAt)
	if err != nil || expires.Before(time.Now().Add(47*time.Hour)) || expires.After(time.Now().Add(49*time.Hour)) {
		t.Fatalf("extended expiry = %q, %v; want about 48h from now", extended.Branch.ExpiresAt, err)
	}
	// 720h from now is past 720h from the branch's creation.
	rec = e.Do(t, http.MethodPost, base+"pr-1/branch/extend", api.ExtendProjectEnvironmentBranchRequest{TTL: "720h"}, nil)
	var problem api.Problem
	if rec.Code != http.StatusConflict || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodeEnvironmentBranchLifetime {
		t.Fatalf("extend past the lifetime = %d %s; want 409 %s", rec.Code, rec.Body, api.CodeEnvironmentBranchLifetime)
	}
	if rec := e.Do(t, http.MethodPost, base+"pr-1/branch/extend", api.ExtendProjectEnvironmentBranchRequest{TTL: "10m"}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("extend below the minimum TTL = %d %s; want 400", rec.Code, rec.Body)
	}

	rec = e.Do(t, http.MethodPost, base+"pr-1/branch/reset", nil, nil)
	reset := decode(t, rec.Code, rec.Body.Bytes(), http.StatusOK)
	if reset.Slug != "pr-1" || reset.ID == created.ID || reset.Branch.Parent != "production" || len(reset.Branch.Workloads) != 1 ||
		reset.Branch.Workloads[0].URL == created.Branch.Workloads[0].URL || reset.Branch.Workloads[0].SourceDeployment != parent.ID {
		t.Fatalf("reset = %+v; want pr-1 made again from production with a new URL", reset)
	}
	// The reset keeps the branch's lifetime: the extended 48 hours.
	resetExpires, err := time.Parse(time.RFC3339Nano, reset.Branch.ExpiresAt)
	if err != nil || resetExpires.Before(time.Now().Add(47*time.Hour)) {
		t.Fatalf("reset expiry = %q, %v; want the branch's 48h lifetime", reset.Branch.ExpiresAt, err)
	}
	branches, err := store.ListProjectEnvironmentBranches(ctx, acct.ID, project.ID)
	if err != nil || len(branches) != 1 || branches[0].EnvironmentID != reset.ID {
		t.Fatalf("branches after reset = %+v, %v; want only the new pr-1", branches, err)
	}
}
