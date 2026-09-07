package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestParseDebugEvidenceSpansSortsSanitizesAndCaps(t *testing.T) {
	input := make([]debugEvidenceSpan, 0, debugEvidenceMaxSpans+1)
	input = append(input,
		debugEvidenceSpan{TraceID: "trace", SpanID: "slow", Name: "db.query", Kind: "client", DurationNanos: 12_000_000, DBStatement: " SELECT  * FROM users WHERE id = 'secret' AND n = 42 ", Status: "error"},
		debugEvidenceSpan{TraceID: "trace", SpanID: "fast", Name: "http.client", Kind: "client", DurationNanos: 1_000_000},
	)
	for i := 0; i < debugEvidenceMaxSpans-1; i++ {
		input = append(input, debugEvidenceSpan{SpanID: "tie-" + string(rune('a'+i%26)), DurationNanos: 2_000_000})
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	spans, truncated := parseDebugEvidenceSpans(raw)
	if !truncated {
		t.Fatal("expected span cap to be reported")
	}
	if len(spans) != debugEvidenceMaxSpans {
		t.Fatalf("got %d spans, want %d", len(spans), debugEvidenceMaxSpans)
	}
	if spans[0].SpanID != "slow" {
		t.Fatalf("slowest span first: got %q", spans[0].SpanID)
	}
	if got, want := spans[0].DBStatement, "SELECT * FROM users WHERE id = ? AND n = ?"; got != want {
		t.Fatalf("sanitized statement = %q, want %q", got, want)
	}
	if strings.Contains(spans[0].DBStatement, "secret") {
		t.Fatal("sanitized statement leaked a literal")
	}
}

func TestParseDebugEvidenceSpansMalformedIsEmpty(t *testing.T) {
	spans, truncated := parseDebugEvidenceSpans([]byte("not-json"))
	if truncated || len(spans) != 0 {
		t.Fatalf("malformed summary = (%v, %v), want empty false", spans, truncated)
	}
}

func TestBuildDebugEvidenceExplanation(t *testing.T) {
	request := api.DebugTelemetryRequestItem{Route: "/checkout"}
	regression := &api.DebugRegressionItem{DeploymentID: "dep", Factor: "1.50", P95MS: 300, P95BaseMS: 200}
	spans := []api.DebugTelemetrySpan{{Name: "db.query", DurationNanos: 25_000_000}}
	got := buildDebugEvidenceExplanation(request, regression, spans)
	if got.Status != "regression_detected" {
		t.Fatalf("status = %q", got.Status)
	}
	if !strings.Contains(got.Headline, "1.50x slower") || !strings.Contains(got.Headline, "db.query (25ms)") {
		t.Fatalf("headline = %q", got.Headline)
	}
	if got.PrimarySpan == nil || got.PrimarySpan.Name != "db.query" {
		t.Fatal("primary span missing")
	}
	unobserved := buildDebugEvidenceExplanation(request, nil, nil)
	if unobserved.Status != "unobserved" || unobserved.PrimarySpan != nil {
		t.Fatalf("unobserved explanation = %+v", unobserved)
	}
}
