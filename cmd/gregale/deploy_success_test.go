package main_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWaitForDeploymentDoesNotAcceptIntermediateLive(t *testing.T) {
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/deployments/dep-1" {
			http.NotFound(w, r)
			return
		}
		if reads.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{
				ID: "dep-1", Status: main.StatusLive,
				StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", Status: main.DeploymentStatusFailed, Error: "post-readiness smoke failed"})
	}))
	defer srv.Close()

	initial := api.DeploymentResponse{ID: "dep-1", Status: "snapshotting", StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`)}
	final, ok := main.WaitForDeploymentReceiptUntil(context.Background(), main.NewClient(srv.URL, "token"), initial, 3*time.Second)
	if !ok || final.Status != main.DeploymentStatusFailed || reads.Load() < 2 {
		t.Fatalf("final=%+v ok=%v reads=%d", final, ok, reads.Load())
	}
}

func TestDefaultDeployWaitTimeoutCoversServerBuildBudget(t *testing.T) {
	want := time.Duration(api.BuildE2ETimeoutSeconds+5*60) * time.Second
	if main.DefaultDeployWaitTimeout != want {
		t.Fatalf("default deploy wait = %s, want %s", main.DefaultDeployWaitTimeout, want)
	}
	if main.DefaultDeployWaitTimeoutSeconds != int(want/time.Second) {
		t.Fatalf("default deploy wait seconds = %d, want %d", main.DefaultDeployWaitTimeoutSeconds, int(want/time.Second))
	}
}

func TestDeploymentWaitResumeCommand(t *testing.T) {
	if got, want := main.DeploymentWaitResumeCommand("dep-1", 90*time.Second), "gregale release wait dep-1 --timeout 90"; got != want {
		t.Fatalf("resume command = %q, want %q", got, want)
	}
	if got, want := main.DeploymentWaitResumeCommand("dep-1", 0), "gregale release wait dep-1 --timeout 1200"; got != want {
		t.Fatalf("zero-deadline resume command = %q, want %q", got, want)
	}
}
