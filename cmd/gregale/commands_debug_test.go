package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdDebugCoverage_RendersObservedSignalRates(t *testing.T) {
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugCoverageResponse{
			AppID: "app-1", Since: "6h", WindowStart: "2026-09-11T00:00:00Z", WindowEnd: "2026-09-11T06:00:00Z",
			PlanRetentionDays: 7, TelemetryRows: 3, RepresentedRequests: 10, ErrorRequests: 2,
			TraceLinked:       api.DebugCoverageSignal{Rows: 2, Requests: 8, RatePct: 80},
			SpanEvidence:      api.DebugCoverageSignal{Rows: 1, Requests: 4, RatePct: 40},
			WakeEvidence:      api.DebugCoverageSignal{Rows: 1, Requests: 2, RatePct: 20},
			GuestEvidence:     api.DebugCoverageSignal{Rows: 1, Requests: 2, RatePct: 20},
			OldestTelemetryAt: "2026-09-11T00:01:00Z", LatestTelemetryAt: "2026-09-11T05:59:00Z",
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

	if code := cmdDebugCoverage([]string{"my-app", "--since", "6h"}); code != 0 {
		t.Fatalf("cmdDebugCoverage() = %d, want 0", code)
	}
	if got.URL.Path != "/v1/apps/my-app/debug/coverage" || got.URL.Query().Get("since") != "6h" {
		t.Fatalf("request = %s?%s, want coverage with since=6h", got.URL.Path, got.URL.RawQuery)
	}
	for _, want := range []string{"represented requests: 10", "trace linked", "80.0%", "observed range"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("coverage output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestCmdDebugRequestsList_SendsFiltersToServer(t *testing.T) {
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugTelemetryListResponse{
			Since: "6h", WindowStart: "2026-09-12T00:00:00Z", WindowEnd: "2026-09-12T06:00:00Z", Complete: false, NextCursor: "next-page",
			Requests: []api.DebugTelemetryRequestItem{{
				ID: "request-1", Route: "GET /checkout", Method: "GET", Status: 200,
				LatencyMS: 42, Count: 7, ReceivedAt: "2026-09-06T10:00:00Z",
			}},
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

	// The slug intentionally appears before the flags. This is the form
	// shown in the command's top-level docs and must not drop the filters.
	if code := cmdDebugRequestsList([]string{
		"my-app", "--since", "6h", "--route", "GET /checkout", "--cursor", "previous-page", "--limit", "50",
	}); code != 0 {
		t.Fatalf("cmdDebugRequestsList() = %d, want 0", code)
	}

	if got.URL.Path != "/v1/apps/my-app/debug/requests" {
		t.Fatalf("request path = %q, want /v1/apps/my-app/debug/requests", got.URL.Path)
	}
	q := got.URL.Query()
	for key, want := range map[string]string{
		"since": "6h", "route": "GET /checkout", "cursor": "previous-page", "limit": "50",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(stdout.String(), "COUNT") || !strings.Contains(stdout.String(), "7") {
		t.Errorf("human output does not show collapsed request count:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "next_cursor=next-page") {
		t.Errorf("human output does not expose next cursor:\n%s", stdout.String())
	}
}

func TestCmdDebugRequestsList_RejectsInvalidLimitBeforeNetwork(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "fp_test")
	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdDebugRequestsList([]string{"my-app", "--limit", "201"}); code != 1 {
		t.Fatalf("cmdDebugRequestsList() = %d, want 1", code)
	}
	if got := readStderr(); !strings.Contains(got, "--limit must be between 1 and 200") {
		t.Errorf("stderr = %q, want limit validation", got)
	}
}

func TestCmdDebugHelp(t *testing.T) {
	_, readStderr, restore := swapIO(t)
	defer restore()

	if code := cmdDebug([]string{"--help"}); code != 0 {
		t.Fatalf("cmdDebug(--help) = %d, want 0", code)
	}
	got := readStderr()
	for _, want := range []string{"usage: gregale debug", "requests list", "requests evidence", "coverage", "regressions", "compare"} {
		if !strings.Contains(got, want) {
			t.Errorf("help missing %q:\n%s", want, got)
		}
	}
}

func TestCmdDebugRequestsEvidenceUsesEvidenceEndpoint(t *testing.T) {
	var got http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
			Request:     api.DebugTelemetryRequestItem{ID: "request-1", Route: "/checkout"},
			Spans:       []api.DebugTelemetrySpan{{SpanID: "span-1", Name: "db.query", DurationNanos: 20_000_000}},
			Explanation: api.DebugEvidenceExplanation{Status: "unobserved", Headline: "No active regression observation is available for this request."},
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

	if code := cmdDebugRequestsEvidence([]string{"my-app", "request-1"}); code != 0 {
		t.Fatalf("cmdDebugRequestsEvidence() = %d, want 0", code)
	}
	if got.URL.Path != "/v1/apps/my-app/debug/requests/request-1/evidence" {
		t.Fatalf("request path = %q", got.URL.Path)
	}
	if !strings.Contains(stdout.String(), `"db.query"`) {
		t.Fatalf("JSON output missing span: %s", stdout.String())
	}
}

func TestCmdDebugRequestsShow_RendersTimelineAndSpans(t *testing.T) {
	traceID := "trace-1"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.DebugRequestEvidenceResponse{
			Request: api.DebugTelemetryRequestItem{
				ID: "request-1", Route: "/checkout", Method: "GET", Status: 502,
				LatencyMS: 640, Count: 2, ColdBoot: true, TraceID: &traceID,
				ReceivedAt: "2026-09-06T10:00:00Z", DeploymentID: "dep-1", WakeID: "wake-1",
			},
			Timeline: []api.DebugTimelineEvent{{
				At: "2026-09-06T10:00:00.100Z", Phase: "wake", Kind: "wake.boot_completed",
				Summary: "guest became ready", DurationMS: 120, Approximate: true,
			}},
			Correlation: api.DebugRequestCorrelation{Stages: []api.DebugRequestCorrelationStage{
				{Phase: "edge", Status: "observed", DurationMS: 640, Approximate: true},
				{Phase: "billing", Status: "missing", Reason: "billed dimensions are not attached to request evidence yet"},
			}},
			Spans: []api.DebugTelemetrySpan{{
				Name: "db.query", Kind: "client", DurationNanos: 20_000_000, Status: "error",
			}},
			Explanation: api.DebugEvidenceExplanation{Headline: "Cold boot dominated request latency."},
			GeneratedAt: "2026-09-06T10:01:00Z",
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

	if code := cmdDebugRequests([]string{"show", "my-app", "request-1"}); code != 0 {
		t.Fatalf("cmdDebugRequests(show) = %d, want 0", code)
	}
	got := stdout.String()
	for _, want := range []string{
		"GET /checkout · HTTP 502 · 640 ms",
		"TIMELINE",
		"~2026-09-06T10:00:00.100Z",
		"wake.boot_completed",
		"CORRELATION",
		"billing",
		"correlation incomplete",
		"SPAN EVIDENCE",
		"db.query",
		"Cold boot dominated request latency.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("human evidence output missing %q:\n%s", want, got)
		}
	}
}
