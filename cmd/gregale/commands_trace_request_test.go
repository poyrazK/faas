package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdTraceResolvesRequestIDToTrace(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	requestID := "0193a1f2-7c4e-7b1a-9f3e-2d5c8b6a4e10"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/my-api/debug/requests/" + requestID + "/evidence":
			upper := strings.ToUpper(traceID)
			_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{Request: api.DebugTelemetryRequestItem{
				ID: "row-1", RequestID: requestID, Route: "/checkout", Method: "POST", Status: 502, TraceID: &upper,
			}})
		case "/v1/account/traces/" + traceID:
			_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
				TraceID: traceID,
				Matches: []api.AccountTraceMatch{{App: "my-api", Request: api.DebugTelemetryRequestItem{ID: "row-1", Route: "/checkout", Method: "POST", Status: 502}}},
				Invocations: []api.AccountTraceInvocation{{App: "worker", ID: "inv-1", Source: "queue", QueueName: "emails", State: "dead_letter", Attempts: 5,
					CreatedAt: "2026-09-22T10:00:00Z"}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	oldJSON := *main.JsonOutput
	*main.JsonOutput = true
	defer func() { *main.JsonOutput = oldJSON }()

	if code := main.CmdTrace([]string{requestID, "--app", "my-api"}); code != 0 {
		t.Fatalf("cmdTrace = %d\n%s", code, stdout.String())
	}
	var got struct {
		ResolvedFrom struct {
			App       string `json:"app"`
			RequestID string `json:"request_id"`
			TraceID   string `json:"trace_id"`
		} `json:"resolved_from"`
		TraceID     string                       `json:"trace_id"`
		Invocations []api.AccountTraceInvocation `json:"invocations"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if got.ResolvedFrom.App != "my-api" || got.ResolvedFrom.RequestID != requestID || got.ResolvedFrom.TraceID != traceID || got.TraceID != traceID {
		t.Fatalf("resolution = %+v trace=%q", got.ResolvedFrom, got.TraceID)
	}
	if len(got.Invocations) != 1 || got.Invocations[0].State != "dead_letter" {
		t.Fatalf("invocations = %+v", got.Invocations)
	}
}

func TestCmdTraceRequestWithoutTraceShowsEvidence(t *testing.T) {
	requestID := "0193a1f2-7c4e-7b1a-9f3e-2d5c8b6a4e10"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/apps/my-api/debug/requests/"+requestID+"/evidence" {
			_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{Request: api.DebugTelemetryRequestItem{
				ID: "row-1", RequestID: requestID, Route: "/checkout", Method: "POST", Status: 502,
			}})
			return
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	stdout, _, restore := main.SwapIO(t)
	defer restore()
	oldJSON := *main.JsonOutput
	*main.JsonOutput = false
	defer func() { *main.JsonOutput = oldJSON }()

	if code := main.CmdTrace([]string{"--app=my-api", requestID}); code != 0 {
		t.Fatalf("cmdTrace = %d\n%s", code, stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"Request " + requestID + " on my-api: showing the request's own evidence: the request carries no trace id.",
		"debug requests explain my-api " + requestID,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdTraceRequestIDNeedsAnApp(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_test")
	t.Chdir(t.TempDir())
	_, readStderr, restore := main.SwapIO(t)
	defer restore()
	oldJSON := *main.JsonOutput
	*main.JsonOutput = false
	defer func() { *main.JsonOutput = oldJSON }()

	if code := main.CmdTrace([]string{"0193a1f2-7c4e-7b1a-9f3e-2d5c8b6a4e10"}); code == 0 {
		t.Fatal("cmdTrace succeeded without an app")
	}
	if !strings.Contains(readStderr(), "pass --app <slug>") {
		t.Fatalf("stderr = %s", readStderr())
	}
	// A malformed 32-hex trace id stays a trace id error, not a request id.
	if code := main.CmdTrace([]string{"00000000000000000000000000000000"}); code == 0 || !strings.Contains(readStderr(), "Invalid trace id") {
		t.Fatalf("zero trace id: stderr = %s", readStderr())
	}
}
