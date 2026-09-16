package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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
				ID: "dep-1", Status: statusLive,
				StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`),
			})
			return
		}
		_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "dep-1", Status: deploymentStatusFailed, Error: "post-readiness smoke failed"})
	}))
	defer srv.Close()

	initial := api.DeploymentResponse{ID: "dep-1", Status: "snapshotting", StageState: json.RawMessage(`{"current":"snapshot_prepare","history":[]}`)}
	final, ok := waitForDeploymentReceiptUntil(context.Background(), NewClient(srv.URL, "token"), initial, 3*time.Second)
	if !ok || final.Status != deploymentStatusFailed || reads.Load() < 2 {
		t.Fatalf("final=%+v ok=%v reads=%d", final, ok, reads.Load())
	}
}
