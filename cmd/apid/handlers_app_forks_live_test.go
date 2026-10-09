package main

// adr: 732
// Live forks: capture the newest running instance now and fork it.

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func liveFork() api.CreateAppForkRequest {
	yes := true
	return api.CreateAppForkRequest{Live: &yes}
}

func TestAppForks_LiveNeedsCaptures(t *testing.T) {
	e := appForkEnv(t, api.PlanPro)
	seedAppTaskDeployment(t, e, "my-api")
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", liveFork(), nil)
	assertProblem(t, rec, http.StatusNotImplemented, api.CodeLiveForksNotEnabled)
}

func TestAppForks_LiveCapturesTheRunningInstance(t *testing.T) {
	e, app, dep := crashEnv(t, api.PlanPro)
	rec := e.do(t, http.MethodPost, "/v1/apps/my-api/forks", liveFork(), nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeLiveForkRefused)

	ins, err := e.store.CreateInstanceWithMode(context.Background(), app.ID, dep.ID, string(state.StateRunning), 256, "node-1", "wake-1", string(state.InstanceModeNormal))
	if err != nil {
		t.Fatal(err)
	}
	rec = e.do(t, http.MethodPost, "/v1/apps/my-api/forks", liveFork(), nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("live fork = %d %s, want 202", rec.Code, rec.Body.String())
	}
	fork := decodeAppFork(t, rec.Body.Bytes())
	if fork.CrashCaptureID == nil || fork.Status != "queued" || fork.DeploymentID != dep.ID {
		t.Fatalf("live fork = %+v, want queued and pinned to a capture", fork)
	}
	capture, err := e.store.CrashCaptureForRestore(context.Background(), *fork.CrashCaptureID)
	if err != nil || capture.Trigger != state.CrashTriggerLiveFork || capture.InstanceID != ins.ID {
		t.Fatalf("capture = %+v, %v; want a live_fork capture of the running instance", capture, err)
	}

	// A crash snapshot fork cannot also ask for a live capture.
	yes := true
	rec = e.do(t, http.MethodPost, "/v1/apps/my-api/crash-snapshots/"+capture.ID+"/fork", api.CreateAppForkRequest{Live: &yes}, nil)
	assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
}
