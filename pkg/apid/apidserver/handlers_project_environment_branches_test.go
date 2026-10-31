package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func environmentBranchRequest(method, environment string, body string) (*http.Request, *httptest.ResponseRecorder) {
	var raw []byte
	if body != "" {
		raw = []byte(body)
	}
	req, rec := projectRequest(method, "/v1/projects/shop/environments/"+environment+"/branches", "shop", raw)
	req.SetPathValue("environment", environment)
	return req, rec
}

// ADR-20261031193635: a branch copies configuration, publishes the parent's
// live build for each workload without booting it, and is retired with its
// deployments on delete or at its TTL.
func TestEnvironmentBranchPublishesParentBuilds(t *testing.T) {
	_, store, acct, project, app := newProjectLifecycleFixture(t)
	ctx := context.Background()
	notifier := &captureNotifier{}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", notifier)
	if err := store.UpsertAppEnvInScope(ctx, acct.ID, app.ID, "production", "MODE", "production"); err != nil {
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

	req, rec := environmentBranchRequest(http.MethodPost, "production", `{"slug":"pr-7"}`)
	srv.createProjectEnvironmentBranch(rec, req, acct)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), api.CodeEnvironmentBranchUnavailable) {
		t.Fatalf("switched off: status=%d body=%s", rec.Code, rec.Body.String())
	}
	srv.runtimeConfig = newRuntimeConfigManager(nil)
	if err := srv.runtimeConfig.apply(runtimeConfigEnvironmentBranches, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}

	for _, body := range []string{`{"slug":"pr-7","ttl":"5m"}`, `{"slug":"pr-7","egress":"contained"}`, `{"slug":"production"}`, `{"slug":"Not A Slug"}`} {
		req, rec := environmentBranchRequest(http.MethodPost, "production", body)
		srv.createProjectEnvironmentBranch(rec, req, acct)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status=%d body=%s", body, rec.Code, rec.Body.String())
		}
	}

	req, rec = environmentBranchRequest(http.MethodPost, "production", `{"slug":"pr-7","ttl":"2h"}`)
	srv.createProjectEnvironmentBranch(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("branch status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ProjectEnvironmentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	b := response.Branch
	if response.Slug != "pr-7" || b == nil || b.Parent != "production" || b.Depth != 1 || b.Egress != api.EnvironmentBranchEgressLive || len(b.Workloads) != 1 {
		t.Fatalf("branch response = %s", rec.Body.String())
	}
	expires, err := time.Parse(time.RFC3339Nano, b.ExpiresAt)
	if err != nil || expires.Before(time.Now().Add(110*time.Minute)) || expires.After(time.Now().Add(130*time.Minute)) {
		t.Fatalf("expires_at = %q, %v", b.ExpiresAt, err)
	}
	workload := b.Workloads[0]
	if workload.Workload != app.Slug || workload.SourceDeployment != parent.ID || workload.Status != string(state.DeploySnapshotting) {
		t.Fatalf("workload = %+v", workload)
	}
	branched, err := store.DeploymentByID(ctx, workload.DeploymentID)
	if err != nil || branched.Scope != "pr-7" || branched.RootfsKey != "layers/shop.ext4" {
		t.Fatalf("branch deployment = %+v, %v", branched, err)
	}
	if lazy, err := store.EnvironmentBranchDeployment(ctx, branched.ID); err != nil || !lazy {
		t.Fatalf("branch deployment lazy = %v, %v", lazy, err)
	}
	primed := false
	for _, n := range notifier.notif {
		primed = primed || n.channel == db.NotifySnapshotPrime && strings.Contains(n.payload, branched.ID)
	}
	if !primed {
		t.Fatalf("no snapshot_prime for %s: %+v", branched.ID, notifier.notif)
	}
	envs, err := store.ListAppEnvInScope(ctx, acct.ID, app.ID, "pr-7")
	if err != nil || len(envs) != 1 || envs[0].Value != "production" {
		t.Fatalf("branch variables = %+v, %v", envs, err)
	}

	req, rec = environmentBranchRequest(http.MethodPost, "production", `{"slug":"pr-7"}`)
	srv.createProjectEnvironmentBranch(rec, req, acct)
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate: status=%d body=%s", rec.Code, rec.Body.String())
	}
	req, rec = environmentBranchRequest(http.MethodGet, "production", "")
	srv.listProjectEnvironmentBranches(rec, req, acct)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"slug":"pr-7"`) {
		t.Fatalf("list: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Delete retires the branch's deployment; the parent keeps serving.
	req, rec = projectRequest(http.MethodDelete, "/v1/projects/shop/environments/pr-7", "shop", nil)
	req.SetPathValue("environment", "pr-7")
	srv.deleteProjectEnvironment(rec, req, acct)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-7"); err == nil {
		t.Fatal("branch environment survived delete")
	}
	if retired, err := store.DeploymentByID(ctx, branched.ID); err != nil || retired.Status != state.DeployCancelled {
		t.Fatalf("retired branch deployment = %+v, %v", retired, err)
	}
	if kept, err := store.DeploymentByID(ctx, parent.ID); err != nil || kept.Status != state.DeployLive {
		t.Fatalf("parent deployment = %+v, %v", kept, err)
	}

	// The sweeper deletes a branch once it expires.
	req, rec = environmentBranchRequest(http.MethodPost, "production", `{"slug":"pr-8","ttl":"1h"}`)
	srv.createProjectEnvironmentBranch(rec, req, acct)
	if rec.Code != http.StatusCreated {
		t.Fatalf("second branch status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := srv.sweepEnvironmentBranches(ctx, store, time.Now().Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-8"); err != nil {
		t.Fatalf("unexpired branch swept: %v", err)
	}
	if err := srv.sweepEnvironmentBranches(ctx, store, time.Now().Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentBySlug(ctx, acct.ID, project.ID, "pr-8"); err == nil {
		t.Fatal("expired branch survived the sweep")
	}
}
