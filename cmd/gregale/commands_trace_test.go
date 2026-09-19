package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdTraceRendersAccountTraceEvidence(t *testing.T) {
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/account/traces/" + traceID:
			_ = json.NewEncoder(w).Encode(api.AccountTraceLookupResponse{
				TraceID: traceID,
				Matches: []api.AccountTraceMatch{
					{App: "alpha", Request: api.DebugTelemetryRequestItem{ID: "row-alpha", Route: "/checkout", Method: "GET", Status: 200, LatencyMS: 100}},
					{App: "beta", Request: api.DebugTelemetryRequestItem{ID: "row-beta", Route: "/checkout", Method: "GET", Status: 200, LatencyMS: 40}},
				},
				Spans: []api.DebugTelemetrySpan{
					{TraceID: traceID, SpanID: "root", Name: "edge.request", Kind: "server", DurationNanos: 100_000_000},
					{TraceID: traceID, SpanID: "guest", ParentSpanID: "root", Name: "guest.request", Kind: "server", DurationNanos: 40_000_000},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdTrace([]string{traceID}); code != 0 {
		t.Fatalf("cmdTrace = %d", code)
	}
	got := stdout.String()
	for _, want := range []string{
		"TRACE " + traceID + " · 2 app match(es) · 0 queue invocation(s) · 2 span(s)",
		"alpha · GET /checkout · HTTP 200 · 100 ms",
		"beta · GET /checkout · HTTP 200 · 40 ms",
		"└─ edge.request [server] 100 ms",
		"└─ guest.request [server] 40.00 ms",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace output missing %q:\n%s", want, got)
		}
	}
}
