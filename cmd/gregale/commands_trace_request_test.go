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

// Request ids are minted by wire.NewRequestID: 32 lowercase hex, the same
// shape as a W3C trace id.
const (
	traceRequestTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	traceRequestID      = "019a3c5e7d1b7f2a9c4e6b8d0f1a2c3e"
)

func writeTraceProblem404(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"type":"about:blank","title":"Not found","status":404,"code":"not_found","detail":"not found"}`))
}

// traceRequestServer serves one request of my-api. traceID is the trace the
// request carries ("" for none); indexed says whether that trace is found.
func traceRequestServer(t *testing.T, traceID string, indexed bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/my-api/debug/requests/" + traceRequestID + "/evidence":
			item := api.DebugTelemetryRequestItem{ID: "row-1", RequestID: traceRequestID, Route: "/checkout", Method: "POST", Status: 502}
			if traceID != "" {
				upper := strings.ToUpper(traceID)
				item.TraceID = &upper
			}
			_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{Request: item})
		case "/v1/account/traces/" + traceRequestTraceID:
			if !indexed {
				writeTraceProblem404(w)
				return
			}
			_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
				TraceID: traceRequestTraceID,
				Matches: []api.AccountTraceMatch{{App: "my-api", Request: api.DebugTelemetryRequestItem{ID: "row-1", Route: "/checkout", Method: "POST", Status: 502}}},
				Invocations: []api.AccountTraceInvocation{{App: "worker", ID: "inv-1", Source: "queue", QueueName: "emails", State: "dead_letter", Attempts: 5,
					CreatedAt: "2026-09-22T10:00:00Z"}},
			})
		default:
			writeTraceProblem404(w)
		}
	}))
}

func useTraceServer(t *testing.T, srv *httptest.Server, asJSON bool) {
	t.Helper()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")
	oldJSON := *main.JsonOutput
	*main.JsonOutput = asJSON
	t.Cleanup(func() { *main.JsonOutput = oldJSON })
}

func TestCmdTraceResolvesRequestIDToTrace(t *testing.T) {
	srv := traceRequestServer(t, traceRequestTraceID, true)
	defer srv.Close()
	useTraceServer(t, srv, true)
	stdout, _, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdTrace([]string{traceRequestID, "--app", "my-api"}); code != 0 {
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
	if got.ResolvedFrom.App != "my-api" || got.ResolvedFrom.RequestID != traceRequestID || got.ResolvedFrom.TraceID != traceRequestTraceID || got.TraceID != traceRequestTraceID {
		t.Fatalf("resolution = %+v trace=%q", got.ResolvedFrom, got.TraceID)
	}
	if len(got.Invocations) != 1 || got.Invocations[0].State != "dead_letter" {
		t.Fatalf("invocations = %+v", got.Invocations)
	}
}

func TestCmdTraceRequestWithoutTraceShowsEvidence(t *testing.T) {
	srv := traceRequestServer(t, "", false)
	defer srv.Close()
	useTraceServer(t, srv, false)
	stdout, _, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdTrace([]string{"--app=my-api", traceRequestID}); code != 0 {
		t.Fatalf("cmdTrace = %d\n%s", code, stdout.String())
	}
	got := stdout.String()
	for _, want := range []string{
		"Request " + traceRequestID + " on my-api: showing the request's own evidence: the request carries no trace id.",
		"debug requests explain my-api " + traceRequestID,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdTraceWatchFallsBackWhenTraceAgedOut(t *testing.T) {
	srv := traceRequestServer(t, traceRequestTraceID, false)
	defer srv.Close()
	useTraceServer(t, srv, true)
	stdout, _, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdTrace([]string{traceRequestID, "--app", "my-api", "--watch", "--timeout", "2s"}); code != 0 {
		t.Fatalf("cmdTrace = %d\n%s", code, stdout.String())
	}
	var got struct {
		ResolvedFrom struct {
			TraceID string `json:"trace_id"`
		} `json:"resolved_from"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if got.ResolvedFrom.TraceID != traceRequestTraceID || !strings.Contains(got.Note, "no longer indexed") {
		t.Fatalf("output = %+v", got)
	}
}

func TestCmdTraceUnknownIDNeedsAnApp(t *testing.T) {
	srv := traceRequestServer(t, "", false)
	defer srv.Close()
	useTraceServer(t, srv, false)
	t.Chdir(t.TempDir())
	_, readStderr, restore := main.SwapIO(t)
	defer restore()

	if code := main.CmdTrace([]string{traceRequestID}); code == 0 {
		t.Fatal("cmdTrace succeeded for an unknown id without an app")
	}
	if !strings.Contains(readStderr(), "pass --app <slug>") {
		t.Fatalf("stderr = %s", readStderr())
	}
	if code := main.CmdTrace([]string{"00000000000000000000000000000000"}); code == 0 || !strings.Contains(readStderr(), "Invalid trace id") {
		t.Fatalf("zero trace id: stderr = %s", readStderr())
	}
}
