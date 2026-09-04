package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
