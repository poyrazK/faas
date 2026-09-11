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

func TestBuildDebugRequestCorrelationMakesMissingSignalsExplicit(t *testing.T) {
	request := api.DebugTelemetryRequestItem{
		ReceivedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		LatencyMS:  12,
		Status:     200,
	}
	timeline := []api.DebugTimelineEvent{
		{At: "2026-09-09T11:59:59.988Z", Kind: "request.received", Phase: "request", Approximate: true},
		{At: "2026-09-09T12:00:00Z", Kind: "request.completed", Phase: "request", Approximate: true},
	}
	got := buildDebugRequestCorrelation(request, timeline, nil)
	if len(got.Stages) != 6 {
		t.Fatalf("stage count = %d, want 6", len(got.Stages))
	}
	if got.Stages[0].Status != "observed" || !got.Stages[0].Approximate || got.Stages[0].DurationMS != 12 {
		t.Fatalf("edge stage = %+v", got.Stages[0])
	}
	if got.Stages[1].Status != "not_applicable" || got.Stages[2].Status != "not_applicable" {
		t.Fatalf("warm wake stages = %+v", got.Stages[1:3])
	}
	if got.Stages[3].Status != "missing" || got.Stages[4].Status != "missing" || got.Stages[5].Status != "missing" {
		t.Fatalf("missing stages = %+v", got.Stages[3:])
	}
	if got.Complete {
		t.Fatal("correlation with missing signals must not be complete")
	}
}

func TestBuildDebugRequestCorrelationComputesQueueWakeAndDownstream(t *testing.T) {
	request := api.DebugTelemetryRequestItem{WakeID: "wake-1"}
	timeline := []api.DebugTimelineEvent{
		{At: "2026-09-09T12:00:00.000Z", Kind: "wake.queue_accepted", Phase: "wake"},
		{At: "2026-09-09T12:00:00.025Z", Kind: "wake.admitted", Phase: "wake"},
		{At: "2026-09-09T12:00:00.050Z", Kind: "wake.boot_started", Phase: "wake"},
		{At: "2026-09-09T12:00:00.200Z", Kind: "wake.readiness_200", Phase: "wake"},
		{At: "2026-09-09T12:00:00.240Z", Kind: "wake.boot_completed", Phase: "wake"},
		{At: "2026-09-09T12:00:00.300Z", Kind: "wake.proxy_first_byte", Phase: "wake"},
	}
	spans := []api.DebugTelemetrySpan{{DurationNanos: 41_000_000}, {DurationNanos: 7_000_000}}
	got := buildDebugRequestCorrelation(request, timeline, spans)
	if got.Stages[1].Status != "observed" || got.Stages[1].DurationMS != 25 {
		t.Fatalf("queue stage = %+v", got.Stages[1])
	}
	if got.Stages[2].Status != "observed" || got.Stages[2].DurationMS != 190 {
		t.Fatalf("wake stage = %+v", got.Stages[2])
	}
	if got.Stages[3].Status != "partial" || got.Stages[3].EvidenceCount != 1 {
		t.Fatalf("guest stage = %+v", got.Stages[3])
	}
	if got.Stages[4].Status != "observed" || got.Stages[4].DurationMS != 41 || got.Stages[4].EvidenceCount != 2 {
		t.Fatalf("downstream stage = %+v", got.Stages[4])
	}
	if got.Complete {
		t.Fatal("partial guest and missing billing signals must not be complete")
	}
}
