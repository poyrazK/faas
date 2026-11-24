package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// A log stream whose connection dies without a FIN sends nothing more, not
// even the 15 s heartbeat. The deploy wait used to sit on it until TCP
// keepalive gave up (~150 s; an MCP redeploy on 2026-11-24 waited 154 s after
// its deployment went live). An idle stream now falls back to the status poll.
func TestDeployStreamIdleFallsBackToStatus(t *testing.T) {
	defer main.SetDeployStreamIdleTimeout(200 * time.Millisecond)()
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"building\"}\n\n")
			w.(http.Flusher).Flush()
			select { // a dead connection: no bytes, no close
			case <-release:
			case <-r.Context().Done():
			}
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	start := time.Now()
	code := main.StreamDeployLogsWait(context.Background(), main.NewClient(srv.URL, "fp_test"),
		api.DeploymentResponse{ID: "d1", Status: "building"}, "demo", 15*time.Second)
	elapsed := time.Since(start)
	restoreErr()
	restoreOut()
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0 for a deployment that went live", code, stderr.String())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait took %s, want the idle stream abandoned after ~200ms", elapsed)
	}
	if !strings.Contains(stderr.String(), "build log stream interrupted") {
		t.Fatalf("stderr = %q, want the interrupted-stream notice", stderr.String())
	}
}

// Heartbeats keep the stream alive past the idle timeout.
func TestDeployStreamHeartbeatsKeepStreamOpen(t *testing.T) {
	defer main.SetDeployStreamIdleTimeout(300 * time.Millisecond)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			for range 8 { // 800 ms of heartbeats, well past the 300 ms idle bound
				_, _ = fmt.Fprint(w, ":\n\n")
				w.(http.Flusher).Flush()
				time.Sleep(100 * time.Millisecond)
			}
			_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	code := main.StreamDeployLogsWait(context.Background(), main.NewClient(srv.URL, "fp_test"),
		api.DeploymentResponse{ID: "d1", Status: "building"}, "demo", 15*time.Second)
	restoreErr()
	restoreOut()
	if code != 0 || strings.Contains(stderr.String(), "interrupted") {
		t.Fatalf("exit=%d stderr=%q, want a clean live result without an interruption", code, stderr.String())
	}
}
