package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugRequestsTrace_RendersParentChildTree(t *testing.T) {
	traceID := "0123456789abcdef0123456789abcdef"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/debug/requests/request-1/evidence" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
			Request: api.DebugTelemetryRequestItem{
				ID: "request-1", Route: "/checkout", Method: "GET", Status: 200,
				LatencyMS: 140, TraceID: &traceID,
			},
			Spans: []api.DebugTelemetrySpan{
				{SpanID: "root", Name: "http.request", Kind: "server", DurationNanos: 140_000_000},
				{SpanID: "db", ParentSpanID: "root", Name: "db.query", Kind: "client", DurationNanos: 12_500_000, DBStatement: "SELECT users WHERE id = ?"},
				{SpanID: "cache", ParentSpanID: "db", Name: "cache.lookup", Kind: "client", DurationNanos: 1_250_000},
				{SpanID: "orphan", ParentSpanID: "evicted", Name: "downstream.call", Kind: "client", DurationNanos: 5_000_000},
			},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRequests([]string{"trace", "my-app", "request-1"}); code != 0 {
		t.Fatalf("cmdDebugRequests(trace) = %d, want 0", code)
	}
	got := stdout.String()
	for _, want := range []string{
		"GET /checkout · HTTP 200 · 140 ms",
		"trace " + traceID,
		"SPAN TREE",
		"├─ http.request [server] 140 ms",
		"│  └─ db.query [client] 12.50 ms · db=SELECT users WHERE id = ?",
		"│     └─ cache.lookup [client] 1.25 ms",
		"└─ downstream.call [client] 5.00 ms · parent omitted",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("trace output missing %q:\n%s", want, got)
		}
	}
}

func TestRenderDebugRequestTrace_JSONIsSafeAndBounded(t *testing.T) {
	traceID := "0123456789abcdef0123456789abcdef"
	resp := api.DebugRequestEvidenceResponse{
		Request:        api.DebugTelemetryRequestItem{ID: "request-1", TraceID: &traceID},
		Spans:          []api.DebugTelemetrySpan{{SpanID: "span-1", Name: "db.query", DBStatement: "SELECT users WHERE id = ?"}},
		SpansTruncated: true,
	}
	var out strings.Builder
	encoded := debugRequestTraceOutput{
		Request:        resp.Request,
		TraceID:        debugRequestTraceID(resp.Request),
		Spans:          resp.Spans,
		SpansTruncated: resp.SpansTruncated,
	}
	if err := json.NewEncoder(&out).Encode(encoded); err != nil {
		t.Fatalf("encode trace output: %v", err)
	}
	got := out.String()
	for _, forbidden := range []string{"attributes", "status_message", "request_body", "headers"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("trace JSON leaked %q: %s", forbidden, got)
		}
	}
	for _, want := range []string{traceID, "spans_truncated", "db.query"} {
		if !strings.Contains(got, want) {
			t.Errorf("trace JSON missing %q: %s", want, got)
		}
	}
}
