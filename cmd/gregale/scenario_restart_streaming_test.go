package main_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

type streamingRestartClient struct {
	requests  int
	onRestart func()
	wakeID    string
}

func (c *streamingRestartClient) RestartAppFresh(context.Context, string) (api.AppRestartResponse, error) {
	c.requests++
	c.onRestart()
	return api.AppRestartResponse{WakeID: c.wakeID}, nil
}

func (c *streamingRestartClient) GetRuntimeConfigRestartStatus(context.Context, string, string) (api.RuntimeConfigRestartStatusResponse, error) {
	at := time.Now().UTC()
	return api.RuntimeConfigRestartStatusResponse{WakeID: c.wakeID, Status: "completed", Attempts: 1, RequestedAt: at.Add(-time.Second), CompletedAt: &at}, nil
}

func (c *streamingRestartClient) ListWakeTimeline(_ context.Context, _, wakeID, _ string, _ int) (api.WakeTimelineResponse, error) {
	return api.WakeTimelineResponse{WakeID: wakeID, Events: []api.WakeTimelineEvent{{Kind: "wake.boot_completed", Data: map[string]any{"method": "cold_boot"}}}}, nil
}

func TestScenarioRestartWaitsForStreamingWriteBody(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		status        int
		pass          bool
	}{
		{"pending body", "canceled_after_restart", 200, true},
		{"unexpected status with pending body", "canceled_after_restart", 503, true},
		{"storage failure", "canceled_on_failure", 200, false},
		{"completed body", "request_ended", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var active, restarted atomic.Bool
			flushed, writeDone := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/write":
					_, _ = io.Copy(io.Discard, r.Body)
					active.Store(true)
					defer close(writeDone)
					defer active.Store(false)
					w.WriteHeader(tc.status)
					w.(http.Flusher).Flush()
					close(flushed)
					if tc.name == "completed body" {
						_, _ = io.WriteString(w, `{"done":true}`)
						return
					}
					<-r.Context().Done()
				case "/checkpoint":
					select {
					case <-flushed:
					case <-r.Context().Done():
						return
					}
					// A headers-only client closes the stream before this checkpoint.
					// A client consuming the body keeps it open through replacement.
					timer := time.NewTimer(50 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-writeDone:
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
					_ = json.NewEncoder(w).Encode(map[string]bool{"pending": active.Load()})
				case "/ledger":
					id := "persisted"
					if tc.name == "storage failure" {
						id = "missing"
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
				case "/ready":
					if !restarted.Load() {
						w.WriteHeader(503)
					}
				}
			}))
			defer server.Close()
			client := &streamingRestartClient{wakeID: "11111111-1111-4111-8111-111111111111"}
			client.onRestart = func() {
				if !active.Load() {
					t.Error("restart submitted after the streaming write ended")
				}
				restarted.Store(true)
			}
			spec := main.TestRestartSpec{
				Workload: "app", Timeout: "2s",
				Readiness:     main.TestHTTPRequest{Name: "ready", Method: "GET", Path: "/ready", Expect: main.TestHTTPExpect{Status: 200}},
				StorageChecks: []main.TestHTTPRequest{{Name: "ledger", Method: "GET", Path: "/ledger", Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/id": "persisted"}}}},
				InFlight: &main.TestRestartInFlightSpec{
					Request:     main.TestHTTPRequest{Name: "write", Method: "POST", Path: "/write", JSON: map[string]any{"operation": "pending"}, Expect: main.TestHTTPExpect{Status: 200}},
					Checkpoints: []main.TestRestartCheckpointSpec{{Workload: "app", Request: main.TestHTTPRequest{Name: "checkpoint", Method: "GET", Path: "/checkpoint", Expect: main.TestHTTPExpect{Status: 200, JSON: map[string]any{"/pending": true}}}}},
				},
			}
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			evidence, err := main.RunTestRestart(ctx, client, server.URL, []string{"GREGALE_TEST_APP_SLUG=app"}, nil, "run", spec, map[string]string{})
			if ctx.Err() != nil {
				t.Fatal("stream cleanup waited for the outer deadline instead of canceling the write")
			}
			if (err == nil) != tc.pass || (evidence.Status == "passed") != tc.pass {
				t.Fatalf("restart passed=%t, want %t: %+v (%v)", err == nil, tc.pass, evidence, err)
			}
			flight := evidence.InFlight
			if flight == nil || !flight.Joined || flight.Outcome != tc.outcome || flight.ResponseStatus != tc.status {
				t.Fatalf("incorrect in-flight evidence: %+v (%v)", flight, err)
			}
			if tc.pass && (client.requests != 1 || !flight.PendingAtRestart || !flight.RestartCompleted) || !tc.pass && client.requests != 0 {
				t.Fatalf("incorrect restart submission: %d requests, %+v", client.requests, flight)
			}
			select {
			case <-writeDone:
			case <-time.After(time.Second):
				t.Fatal("streaming write was not closed during cleanup")
			}
		})
	}
}
