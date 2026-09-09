package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
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

func TestBuildDebugRequestTimelineJoinsWakeAndMarksError(t *testing.T) {
	store := state.NewMemStore()
	appID := "app-timeline"
	wakeID := "wake-timeline"
	base := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	for i, kind := range []string{"wake.queue_accepted", "wake.boot_started", "wake.boot_completed"} {
		at := base.Add(1300 * time.Millisecond).Add(time.Duration(i) * 100 * time.Millisecond)
		payload := `{"wake_id":"` + wakeID + `","app_id":"` + appID + `"}`
		if err := store.AppendEventAt(context.Background(), "schedd", kind, nil, []byte(payload), at); err != nil {
			t.Fatalf("AppendEventAt(%s): %v", kind, err)
		}
	}
	request := api.DebugTelemetryRequestItem{
		ReceivedAt: base.Add(2 * time.Second).Format(time.RFC3339Nano),
		LatencyMS:  800,
		Status:     502,
		Route:      "GET /checkout",
		WakeID:     wakeID,
	}
	got, err := (&server{store: store}).buildDebugRequestTimeline(context.Background(), appID, request, nil)
	if err != nil {
		t.Fatalf("buildDebugRequestTimeline: %v", err)
	}
	if len(got) != 6 { // received + 3 wake events + completed + error
		t.Fatalf("timeline length = %d, want 6: %+v", len(got), got)
	}
	if got[0].Kind != "request.received" || got[1].Kind != "wake.queue_accepted" || got[3].Kind != "wake.boot_completed" {
		t.Fatalf("timeline ordering = %+v", got)
	}
	if got[len(got)-1].Kind != "request.error" || got[len(got)-1].Status != 502 {
		t.Fatalf("error marker = %+v", got[len(got)-1])
	}
}

func TestBuildDebugRequestTimelineWithoutWake(t *testing.T) {
	request := api.DebugTelemetryRequestItem{
		ReceivedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		LatencyMS:  12,
		Status:     200,
	}
	got, err := (&server{}).buildDebugRequestTimeline(context.Background(), "app", request, nil)
	if err != nil {
		t.Fatalf("buildDebugRequestTimeline: %v", err)
	}
	if len(got) != 2 || got[0].Kind != "request.received" || got[1].Kind != "request.completed" {
		t.Fatalf("warm timeline = %+v", got)
	}
}
