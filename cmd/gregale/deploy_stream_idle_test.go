package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func deployStreamServer(t *testing.T, logs http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/v1/deployments/d1/logs"):
			w.Header().Set("Content-Type", "text/event-stream")
			logs(w, r)
		case r.URL.Path == "/v1/deployments/d1":
			_ = json.NewEncoder(w).Encode(api.DeploymentResponse{ID: "d1", Status: "live"})
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitDeploy(t *testing.T, srv *httptest.Server, idle time.Duration) (int, string, time.Duration) {
	t.Helper()
	_, restoreOut := main.CaptureStdout(t)
	stderr, restoreErr := main.CaptureStderr(t)
	start := time.Now()
	code := main.StreamDeployLogsWait(context.Background(), main.NewClient(srv.URL, "fp_test"),
		api.DeploymentResponse{ID: "d1", Status: "building"}, "demo", 15*time.Second, idle)
	elapsed := time.Since(start)
	restoreErr()
	restoreOut()
	return code, stderr.String(), elapsed
}

// A log stream whose connection dies without a FIN sends nothing more, not
// even the 15 s heartbeat. The deploy wait used to sit on it until TCP
// keepalive gave up (~150 s; an MCP redeploy on 2026-11-24 waited 154 s after
// its deployment went live). An idle stream now falls back to the status poll.
func TestDeployStreamIdleFallsBackToStatus(t *testing.T) {
	release := make(chan struct{})
	srv := deployStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "event: log\ndata: {\"line\":\"building\"}\n\n")
		w.(http.Flusher).Flush()
		select { // a dead connection: no bytes, no close
		case <-release:
		case <-r.Context().Done():
		}
	})
	t.Cleanup(func() { close(release) }) // runs before srv.Close (LIFO)
	code, stderr, elapsed := waitDeploy(t, srv, 200*time.Millisecond)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q, want 0 for a deployment that went live", code, stderr)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("wait took %s, want the idle stream abandoned after ~200ms", elapsed)
	}
	if !strings.Contains(stderr, "no data or heartbeat from the server for 200ms") {
		t.Fatalf("stderr = %q, want the watchdog named as the cause", stderr)
	}
}

// The watchdog times the server, not the consumer: pausing between Reads of
// data already available does not close the stream.
func TestIdleWatchdogIgnoresSlowConsumer(t *testing.T) {
	w := main.NewIdleWatchdogReader(io.NopCloser(strings.NewReader("abc")), 50*time.Millisecond)
	defer func() { _ = w.Close() }()
	buf := make([]byte, 1)
	for range 3 {
		time.Sleep(120 * time.Millisecond)
		if _, err := w.Read(buf); err != nil {
			t.Fatalf("read after a consumer pause: %v", err)
		}
	}
}

func TestIdleWatchdogWrapsCause(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	w := main.NewIdleWatchdogReader(pr, 20*time.Millisecond)
	_, err := w.Read(make([]byte, 1))
	if !errors.Is(err, main.ErrDeployStreamIdle) || !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("err = %v, want the idle error wrapping the close error", err)
	}
}

// Heartbeats keep the stream alive past the idle bound.
func TestDeployStreamHeartbeatsKeepStreamOpen(t *testing.T) {
	srv := deployStreamServer(t, func(w http.ResponseWriter, _ *http.Request) {
		for range 50 { // 2.5 s of heartbeats, well past the 1 s idle bound
			_, _ = fmt.Fprint(w, ":\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
		_, _ = fmt.Fprint(w, "event: status\ndata: {\"status\":\"live\"}\n\n")
	})
	code, stderr, _ := waitDeploy(t, srv, time.Second)
	if code != 0 || strings.Contains(stderr, "interrupted") {
		t.Fatalf("exit=%d stderr=%q, want a clean live result without an interruption", code, stderr)
	}
}
