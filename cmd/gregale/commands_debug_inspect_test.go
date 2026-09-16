package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugRequestsInspect_SelectsLatestAndRendersInvestigation(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/apps/my-app/debug/requests":
			if got := r.URL.Query().Get("limit"); got != "1" {
				t.Errorf("selection limit = %q, want 1", got)
			}
			for key, want := range map[string]string{
				"since": "6h", "route": "GET /checkout", "status": "500", "min_latency_ms": "250",
			} {
				if got := r.URL.Query().Get(key); got != want {
					t.Errorf("selection query %s = %q, want %q", key, got, want)
				}
			}
			_ = json.NewEncoder(w).Encode(api.DebugTelemetryListResponse{
				Since: "6h",
				Requests: []api.DebugTelemetryRequestItem{{
					ID: "request-new", Route: "GET /checkout", Method: "GET", Status: 500,
					LatencyMS: 640, Count: 1, ReceivedAt: "2026-09-16T12:00:00Z",
				}},
			})
		case "/v1/apps/my-app/debug/requests/request-new/evidence":
			traceID := "0123456789abcdef0123456789abcdef"
			_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
				Request: api.DebugTelemetryRequestItem{
					ID: "request-new", Route: "GET /checkout", Method: "GET", Status: 500,
					LatencyMS: 640, TraceID: &traceID,
				},
				Spans: []api.DebugTelemetrySpan{
					{SpanID: "root", Name: "http.request", Kind: "server", DurationNanos: 640_000_000},
					{SpanID: "db", ParentSpanID: "root", Name: "db.query", Kind: "client", DurationNanos: 120_000_000},
				},
				Explanation: api.DebugEvidenceExplanation{Headline: "Database span dominates the request."},
			})
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
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

	if code := cmdDebugRequests([]string{
		"inspect", "my-app", "--latest", "--since", "6h", "--route", "GET /checkout",
		"--status", "500", "--min-latency-ms", "250",
	}); code != 0 {
		t.Fatalf("cmdDebugRequests(inspect) = %d, want 0", code)
	}
	if len(paths) != 2 || paths[1] != "/v1/apps/my-app/debug/requests/request-new/evidence" {
		t.Fatalf("request paths = %v, want selection followed by evidence", paths)
	}
	got := stdout.String()
	for _, want := range []string{
		"Selected latest matching request: request-new",
		"Database span dominates the request.",
		"SPAN TREE",
		"└─ http.request [server] 640 ms",
		"   └─ db.query [client] 120 ms",
		"NEXT COMMANDS",
		"gregale debug requests replay my-app request-new --wait",
		"gregale debug bundle my-app request-new --output debug-bundle.json",
		"gregale debug compare my-app --source <source-deployment-id> --mirror <mirror-deployment-id>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("inspect output missing %q:\n%s", want, got)
		}
	}
}

func TestCmdDebugRequestsInspect_JSONIncludesEvidenceTreeAndActions(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/my-app/debug/requests/request-1/evidence" {
			t.Fatalf("request path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
			Request: api.DebugTelemetryRequestItem{ID: "request-1", Route: "/health", Method: "GET", Status: 200},
			Spans:   []api.DebugTelemetrySpan{{SpanID: "span-1", Name: "health.check", Kind: "server", DurationNanos: 2_000_000}},
		})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_test")

	stdout, _, restore := swapIO(t)
	defer restore()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	if code := cmdDebugRequests([]string{"inspect", "my-app", "request-1"}); code != 0 {
		t.Fatalf("cmdDebugRequests(inspect exact) = %d, want 0", code)
	}
	var got debugRequestInspectOutput
	if err := json.Unmarshal([]byte(stdout.String()), &got); err != nil {
		t.Fatalf("inspect JSON: %v\n%s", err, stdout.String())
	}
	if got.Selection.Mode != "request_id" || got.Selection.RequestID != "request-1" {
		t.Fatalf("selection = %+v", got.Selection)
	}
	if len(got.SpanTree) != 1 || got.SpanTree[0].Span == nil || got.SpanTree[0].Span.Name != "health.check" {
		t.Fatalf("span tree = %+v", got.SpanTree)
	}
	if len(got.NextActions) != 3 {
		t.Fatalf("next actions = %+v, want three command hints", got.NextActions)
	}
}
