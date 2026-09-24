package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAdvanceCanaryDerivesStageAndRejectsStaleWorker(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "canary-advance"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID:            app.ID,
		ImageDigest:      "sha256:canary",
		CanaryPreset:     "balanced",
		CanaryStep:       0,
		CanaryTotalSteps: 4,
		RolloutState:     "pending",
		TrafficPercent:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, http.MethodPost, "/v1/deployments/"+canary.ID+"/canary/advance",
		api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("advance status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out api.CanaryAdvanceResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Deployment.CanaryStep != 1 || out.Deployment.TrafficPercent != 10 || out.Deployment.RolloutState != "rolling_out" {
		t.Fatalf("advance response = %+v, want step=1 traffic=10 rolling_out", out.Deployment)
	}
	if out.AuditID == "" {
		t.Fatal("advance response audit_id is empty")
	}
	if got, err := e.store.DeploymentByID(ctx, prior.ID); err != nil || got.TrafficPercent != 90 {
		t.Fatalf("prior after advance = %+v, %v; want traffic=90", got, err)
	}

	stale := e.do(t, http.MethodPost, "/v1/deployments/"+canary.ID+"/canary/advance",
		api.AdvanceCanaryRequest{ExpectedStep: 0}, nil)
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale status = %d, body=%s", stale.Code, stale.Body.String())
	}
	var problem api.Problem
	if err := json.Unmarshal(stale.Body.Bytes(), &problem); err != nil {
		t.Fatal(err)
	}
	if problem.Code != api.CodeCanaryStepConflict {
		t.Fatalf("stale problem code = %q, want %q", problem.Code, api.CodeCanaryStepConflict)
	}
	if _, _, err := e.store.AdvanceCanary(ctx, canary.ID, state.CanaryAdvanceParams{
		ExpectedStep: 0, TrafficPercent: 10,
	}); !errors.Is(err, state.ErrCanaryStepConflict) {
		t.Fatalf("direct stale transition error = %v, want ErrCanaryStepConflict", err)
	}
}

func TestInternalSafeDeployCrossAccountAndPublicIsolation(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	other, err := e.store.CreateAccount(ctx, "canary-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "other-account-canary"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	canary, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, ImageDigest: "sha256:canary", CanaryPreset: "balanced",
		CanaryStep: 0, CanaryTotalSteps: 4, RolloutState: "pending", TrafficPercent: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, canary.ID); err != nil {
		t.Fatal(err)
	}
	publicPath := "/v1/deployments/" + canary.ID + "/canary/advance"
	if rec := e.do(t, http.MethodPost, publicPath, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account customer bearer status = %d, want 404", rec.Code)
	}
	internalPath := "/v1/internal/safe-deploy/deployments/" + canary.ID + "/canary/advance"
	if rec := e.do(t, http.MethodPost, internalPath, api.AdvanceCanaryRequest{ExpectedStep: 0}, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("internal route on public mux status = %d, want 404", rec.Code)
	}

	canaryToken := "canary-service-secret-0000000000000001"
	actionToken := "action-service-secret-0000000000000001"
	for _, tt := range []struct{ addr, canary, action string }{
		{addr: "0.0.0.0:9101", canary: canaryToken, action: actionToken},
		{addr: "127.0.0.1:9101", canary: "short", action: actionToken},
		{addr: "127.0.0.1:9101", canary: canaryToken, action: canaryToken},
	} {
		if err := e.s.mountInternalSafeDeploy(http.NewServeMux(), tt.addr, tt.canary, tt.action); err == nil {
			t.Fatalf("accepted invalid operator listener/service credentials: %+v", tt)
		}
	}
	mux := http.NewServeMux()
	if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	checkDenied := func(token, remote string, want int) {
		t.Helper()
		body, _ := json.Marshal(api.AdvanceCanaryRequest{ExpectedStep: 0})
		req := httptest.NewRequest(http.MethodPost, internalPath, bytes.NewReader(body))
		req.RemoteAddr = remote
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("token/remote %q/%q: status=%d, want %d", token, remote, rec.Code, want)
		}
	}
	checkDenied(e.key, "127.0.0.1:1234", http.StatusUnauthorized)
	checkDenied(actionToken, "127.0.0.1:1234", http.StatusUnauthorized)
	checkDenied(canaryToken, "192.0.2.10:1234", http.StatusForbidden)
	client := api.NewInternalSafeDeployClient(server.URL, canaryToken, actionToken)
	advanced, err := client.AdvanceCanary(ctx, canary.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if advanced.Deployment.CanaryStep != 1 || advanced.Deployment.TrafficPercent != 10 {
		t.Fatalf("advanced deployment = %+v", advanced.Deployment)
	}
	if _, err := client.AdvanceCanary(ctx, canary.ID, 0); err == nil {
		t.Fatal("stale step unexpectedly advanced twice")
	}
	recoveryPath := "/v1/internal/safe-deploy/apps/" + app.Slug + "/rollouts/recover"
	for _, authorization := range []string{"Bearer " + canaryToken, actionToken} {
		req := httptest.NewRequest(http.MethodPost, recoveryPath, bytes.NewBufferString(`{"action":"abort","reason":"scope check"}`))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", authorization)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("canary/raw token on recovery route status = %d, want 401", rec.Code)
		}
	}
	recovered, err := client.RecoverRollout(ctx, app.Slug, "abort", "test service recovery")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Deployment.RolloutState != "aborted" {
		t.Fatalf("recovered state = %q, want aborted", recovered.Deployment.RolloutState)
	}
}

func TestInternalSafeDeployRollbackAcrossAccount(t *testing.T) {
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	other, err := e.store.CreateAccount(ctx, "rollback-other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "other-account-rollback"})
	if err != nil {
		t.Fatal(err)
	}
	prior, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:prior"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.SetDeploymentRootfs(ctx, prior.ID, "/srv/fc/apps/"+app.Slug+"/"+prior.ID+".ext4", "apps/"+app.Slug+"/"+prior.ID+".ext4", 1); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}
	current, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, ImageDigest: "sha256:current"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentLive(ctx, current.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkDeploymentSuperseded(ctx, prior.ID); err != nil {
		t.Fatal(err)
	}

	const canaryToken = "canary-service-secret-0000000000000001"
	const actionToken = "action-service-secret-0000000000000001"
	mux := http.NewServeMux()
	if err := e.s.mountInternalSafeDeploy(mux, "127.0.0.1:9101", canaryToken, actionToken); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	client := api.NewInternalSafeDeployClient(server.URL, canaryToken, actionToken)
	queued, err := client.RollbackToWithRuleAndIdempotencyKey(ctx, app.Slug, prior.ID, "", "internal-cross-account-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if queued.ID != prior.ID || queued.Status != string(state.DeploySnapshotting) {
		t.Fatalf("rollback response = %+v, want prior deployment queued for snapshot validation", queued)
	}
	stillLive, err := e.store.DeploymentByID(ctx, current.ID)
	if err != nil || stillLive.Status != state.DeployLive {
		t.Fatalf("current deployment = %+v, err=%v; must stay live until rollback validates", stillLive, err)
	}
}
