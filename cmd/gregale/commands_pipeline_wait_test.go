package main_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPipelineResumeWaitObservesCurrentRevision(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test-token")
	id := uuid.NewString()
	var mu sync.Mutex
	reads, controls := 0, 0
	p := api.DataPipeline{ID: id, State: "blocked", DesiredState: "running", Reason: "source_authentication_failed", Revision: 4, ObservedRevision: 4, ObservationFresh: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines/" + id:
			reads++
			if controls > 0 {
				p.State, p.Reason, p.ObservationFresh, p.ObservedRevision = "live", "", true, p.Revision
			}
			main.WriteJSONTest(w, p)
		case "POST /v1/data/pipelines/" + id + "/control":
			var request api.ControlDataPipelineRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Action != "resume" || request.ExpectedRevision != p.Revision {
				t.Error("invalid resume request", request, err)
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			controls++
			p.Revision++
			p.ObservationFresh = false
			main.WriteJSONTest(w, p)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	code := main.CmdPipeline([]string{"resume", id, "--wait", "--timeout", "3s"})
	mu.Lock()
	defer mu.Unlock()
	if code != 0 || reads != 2 || controls != 1 {
		t.Fatal("resume did not wait for the new observation", code, reads, controls)
	}
}

func TestPipelineWaitRequiresCurrentBlockedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, state, reason, verification string
		fresh, terminal                   bool
		observed                          int64
	}{
		{"old authentication failure", "blocked", "source_authentication_failed", "", false, false, 1},
		{"expired admission failure", "blocked", "admission_closed", "", false, false, 2},
		{"durable resync observation", "resync_required", "source_schema_changed", "", false, true, 2},
		{"current authentication failure", "blocked", "source_authentication_failed", "", true, true, 2},
		{"authentication failure during verification", "blocked", "source_authentication_failed", "pending", true, true, 2},
		{"current resync observation", "resync_required", "source_schema_changed", "", true, true, 2},
		{"schema API refresh in progress", "blocked", "schema_api_refresh_pending", "passed", true, false, 2},
		{"schema API refresh failed", "blocked", "schema_api_refresh_failed", "passed", true, true, 2},
		{"transient database outage", "blocked", "database_unavailable", "", true, false, 2},
		{"source rewrite verification running", "blocked", "source_storage_verification", "pending", true, false, 2},
		{"source rewrite verification limited", "blocked", "source_storage_verification", "inconclusive", true, true, 2},
		{"source rewrite verification cancelled", "blocked", "source_storage_verification", "unknown", true, true, 2},
		{"continuity verification running", "blocked", "replication_continuity_verification", "pending", true, false, 2},
		{"continuity verification limited", "blocked", "replication_continuity_verification", "inconclusive", true, true, 2},
		{"continuity verification cancelled", "blocked", "replication_continuity_verification", "unknown", true, true, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := api.DataPipeline{ID: uuid.NewString(), State: tc.state, Reason: tc.reason, Verification: tc.verification, Revision: 2, ObservedRevision: tc.observed, ObservationFresh: tc.fresh}
			reader := &pipelineStatusReader{next: api.DataPipeline{ID: p.ID, State: "live", Revision: 2, ObservedRevision: 2, ObservationFresh: true}}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err := main.WaitDataPipeline(ctx, reader, p, "live", time.Millisecond)
			if tc.terminal && (err == nil || reader.calls != 0) {
				t.Fatal("current terminal failure was ignored", err, reader.calls)
			}
			if !tc.terminal && (err != nil || reader.calls != 1) {
				t.Fatal("obsolete or transient failure ended the wait", err, reader.calls)
			}
		})
	}
	for _, reason := range []string{"admission_closed", "schema_api_refresh_pending"} {
		t.Run(reason+" remains bounded by the deadline", func(t *testing.T) {
			p := api.DataPipeline{ID: uuid.NewString(), State: "blocked", Reason: reason, Revision: 2, ObservedRevision: 1}
			if reason == "schema_api_refresh_pending" {
				p.ObservationFresh, p.ObservedRevision = true, p.Revision
			}
			reader := &pipelineStatusReader{next: p}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
			defer cancel()
			if _, err := main.WaitDataPipeline(ctx, reader, p, "live", time.Millisecond); !errors.Is(err, context.DeadlineExceeded) || reader.calls == 0 {
				t.Fatal("wait ignored deadline or skipped polling", err, reader.calls)
			}
		})
	}
}

func TestPipelineResumeWaitPollsSchemaAPIRefresh(t *testing.T) {
	main.ResetJSONOut(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "local-test-token")
	id := uuid.NewString()
	var mu sync.Mutex
	reads, controls := 0, 0
	p := api.DataPipeline{ID: id, State: "paused", DesiredState: "paused", Revision: 1, ObservedRevision: 1, ObservationFresh: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/data/pipelines/" + id:
			reads++
			if controls > 0 {
				p.ObservationFresh, p.ObservedRevision = true, p.Revision
				p.State, p.Reason = "blocked", "schema_api_refresh_pending"
				if reads > 2 {
					p.State, p.Reason = "live", ""
				}
			}
			main.WriteJSONTest(w, p)
		case "POST /v1/data/pipelines/" + id + "/control":
			controls++
			p.Revision++
			p.DesiredState, p.ObservationFresh = "running", false
			main.WriteJSONTest(w, p)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	code := main.CmdPipeline([]string{"resume", id, "--wait", "--timeout", "4s"})
	mu.Lock()
	defer mu.Unlock()
	if code != 0 || reads != 3 || controls != 1 {
		t.Error("healthy approved workflow progress ended the CLI wait as a terminal failure")
	}
}
