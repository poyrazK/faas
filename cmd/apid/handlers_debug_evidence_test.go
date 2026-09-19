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

func TestNormalizeDebugRequestIdentifier(t *testing.T) {
	for _, raw := range []string{
		"0123456789abcdef0123456789abcdef",
		"00000000-0000-0000-0000-000000000001",
		"  public-request-id  ",
	} {
		got, err := normalizeDebugRequestIdentifier(raw)
		if err != nil || got != strings.TrimSpace(raw) {
			t.Fatalf("normalize(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := normalizeDebugRequestIdentifier("   "); err == nil {
		t.Fatal("empty request identifier must be rejected")
	}
	if _, err := normalizeDebugRequestIdentifier(strings.Repeat("x", debugRequestIdentifierMaxBytes+1)); err == nil {
		t.Fatal("oversized request identifier must be rejected")
	}
}

func TestParseDebugEvidenceSpansSortsSanitizesAndCaps(t *testing.T) {
	input := make([]debugEvidenceSpan, 0, debugEvidenceMaxSpans+1)
	input = append(input,
		debugEvidenceSpan{TraceID: "trace", SpanID: "slow", Name: "db.query", Kind: "client", DurationNanos: 12_000_000, DBStatement: " SELECT  * FROM users WHERE id = 'secret' AND n = 42 ", Status: "error", Attributes: map[string]string{
			"gregale.dependency.type": "managed_binding",
			"gregale.dependency.kind": "managed_postgres",
			"db.statement":            "SELECT * FROM users WHERE id = 'secret'",
		}},
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
	if spans[0].DependencyType != "managed_binding" || spans[0].DependencyKind != "managed_postgres" {
		t.Fatalf("dependency classification = (%q, %q)", spans[0].DependencyType, spans[0].DependencyKind)
	}
}

func TestBuildDebugDependencyLatencyAggregatesAndCaps(t *testing.T) {
	spans := []api.DebugTelemetrySpan{
		{DependencyType: "managed_binding", DependencyKind: "managed_postgres", Name: "HTTP GET", DurationNanos: 80_000_000, Status: "error"},
		{DependencyType: "managed_binding", DependencyKind: "managed_postgres", Name: "HTTP GET", DurationNanos: 20_000_000},
		{DependencyType: "guest_transport", DependencyKind: "vmmd_guest_bridge", Name: "HTTP POST", DurationNanos: 120_000_000},
	}
	got, truncated := buildDebugDependencyLatency(spans)
	if truncated || len(got) != 2 {
		t.Fatalf("aggregates = (%+v, %v), want two groups", got, truncated)
	}
	if got[0].Type != "guest_transport" || got[0].MaxDurationMS != 120 || got[0].Calls != 1 {
		t.Fatalf("slowest dependency = %+v", got[0])
	}
	if got[1].Type != "managed_binding" || got[1].Calls != 2 || got[1].Errors != 1 || got[1].TotalDurationMS != 100 || got[1].MaxDurationMS != 80 {
		t.Fatalf("aggregated binding = %+v", got[1])
	}

	many := make([]api.DebugTelemetrySpan, 0, debugDependencyLatencyMax+1)
	for i := 0; i < debugDependencyLatencyMax+1; i++ {
		many = append(many, api.DebugTelemetrySpan{Name: "span-" + string(rune('a'+i)), DurationNanos: uint64(i+1) * uint64(time.Millisecond)})
	}
	got, truncated = buildDebugDependencyLatency(many)
	if !truncated || len(got) != debugDependencyLatencyMax {
		t.Fatalf("cap = (%d, %v), want (%d, true)", len(got), truncated, debugDependencyLatencyMax)
	}
}

func TestParseDebugEvidenceSpansMalformedIsEmpty(t *testing.T) {
	spans, truncated := parseDebugEvidenceSpans([]byte("not-json"))
	if truncated || len(spans) != 0 {
		t.Fatalf("malformed summary = (%v, %v), want empty false", spans, truncated)
	}
}

func TestParseDebugEvidenceSpansPreservesTiming(t *testing.T) {
	start := time.Date(2026, 9, 19, 12, 0, 0, 123000000, time.UTC)
	end := start.Add(42 * time.Millisecond)
	raw, err := json.Marshal([]debugEvidenceSpan{{
		SpanID:            "timed",
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(end.UnixNano()),
		DurationNanos:     uint64(end.Sub(start)),
	}})
	if err != nil {
		t.Fatal(err)
	}

	spans, truncated := parseDebugEvidenceSpans(raw)
	if truncated || len(spans) != 1 {
		t.Fatalf("timed spans = (%v, %v), want one span", spans, truncated)
	}
	if spans[0].StartTime != start.Format(time.RFC3339Nano) || spans[0].EndTime != end.Format(time.RFC3339Nano) {
		t.Fatalf("timing = (%q, %q), want (%q, %q)", spans[0].StartTime, spans[0].EndTime, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
	}
}

func TestBuildDebugWaterfallOrdersAndNests(t *testing.T) {
	base := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	spans := []api.DebugTelemetrySpan{
		{
			SpanID:        "child",
			ParentSpanID:  "root",
			Name:          "db.query",
			StartTime:     base.Add(20 * time.Millisecond).Format(time.RFC3339Nano),
			EndTime:       base.Add(45 * time.Millisecond).Format(time.RFC3339Nano),
			DurationNanos: uint64(25 * time.Millisecond),
		},
		{
			SpanID:        "root",
			Name:          "request",
			StartTime:     base.Format(time.RFC3339Nano),
			EndTime:       base.Add(100 * time.Millisecond).Format(time.RFC3339Nano),
			DurationNanos: uint64(100 * time.Millisecond),
		},
	}

	got, complete := buildDebugWaterfall(spans)
	if !complete || len(got) != 2 {
		t.Fatalf("waterfall = (%+v, %v), want two complete spans", got, complete)
	}
	if got[0].SpanID != "root" || got[1].SpanID != "child" || got[1].Depth != 1 {
		t.Fatalf("waterfall order/depth = %+v", got)
	}
	if got[1].OffsetPct != "20.00" || got[1].WidthPct != "25.00" {
		t.Fatalf("child geometry = (%q, %q), want (20.00, 25.00)", got[1].OffsetPct, got[1].WidthPct)
	}
}

func TestBuildDebugWaterfallMarksPartialMissingTiming(t *testing.T) {
	spans := []api.DebugTelemetrySpan{
		{
			SpanID:    "timed",
			StartTime: "2026-09-19T12:00:00Z",
			EndTime:   "2026-09-19T12:00:00.010Z",
		},
		{SpanID: "untimed"},
	}

	got, complete := buildDebugWaterfall(spans)
	if complete || len(got) != 1 || got[0].SpanID != "timed" {
		t.Fatalf("partial waterfall = (%+v, %v), want one incomplete span", got, complete)
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

func TestBuildDebugEvidenceDegradedExplanation(t *testing.T) {
	spans := []api.DebugTelemetrySpan{{Name: "db.query", DurationNanos: 25_000_000}}
	got := buildDebugEvidenceDegradedExplanation(spans)
	if got.Status != "regression_unavailable" {
		t.Fatalf("status = %q, want regression_unavailable", got.Status)
	}
	if !strings.Contains(got.Headline, "request evidence is otherwise complete") {
		t.Fatalf("headline = %q", got.Headline)
	}
	if got.PrimarySpan == nil || got.PrimarySpan.Name != "db.query" {
		t.Fatalf("primary span = %+v, want db.query", got.PrimarySpan)
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

func TestBuildDebugRequestTimelineAnchorsCollapsedCompletionAfterLinkedWake(t *testing.T) {
	store := state.NewMemStore()
	appID := "app-collapsed-order"
	wakeID := "wake-collapsed-order"
	recordedBucket := time.Date(2026, 9, 16, 22, 52, 0, 0, time.UTC)
	wakeTimes := []time.Time{
		recordedBucket.Add(17*time.Second + 813*time.Millisecond),
		recordedBucket.Add(17*time.Second + 819*time.Millisecond),
		recordedBucket.Add(18*time.Second + 11*time.Millisecond),
	}
	for index, kind := range []string{"wake.queue_accepted", "wake.boot_started", "wake.proxy_first_byte"} {
		payload := `{"wake_id":"` + wakeID + `","app_id":"` + appID + `"}`
		if err := store.AppendEventAt(context.Background(), "schedd", kind, nil, []byte(payload), wakeTimes[index]); err != nil {
			t.Fatalf("AppendEventAt(%s): %v", kind, err)
		}
	}
	request := api.DebugTelemetryRequestItem{
		ReceivedAt: recordedBucket.Format(time.RFC3339Nano),
		LatencyMS:  250,
		Status:     200,
		WakeID:     wakeID,
	}
	timeline, err := (&server{store: store}).buildDebugRequestTimeline(context.Background(), appID, request, nil)
	if err != nil {
		t.Fatalf("buildDebugRequestTimeline: %v", err)
	}
	var completion time.Time
	for _, event := range timeline {
		if event.Kind == "request.completed" {
			completion, err = time.Parse(time.RFC3339Nano, event.At)
			if err != nil {
				t.Fatalf("parse completion: %v", err)
			}
			if !event.Approximate || !strings.Contains(event.Summary, "anchored after linked wake evidence") {
				t.Fatalf("completion marker = %+v", event)
			}
		}
	}
	if completion.IsZero() || !completion.After(wakeTimes[len(wakeTimes)-1]) {
		t.Fatalf("completion %s must follow proxy-first-byte %s; timeline=%+v", completion, wakeTimes[len(wakeTimes)-1], timeline)
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

func TestBuildDebugRequestTimelineIncludesGuestEvidence(t *testing.T) {
	request := api.DebugTelemetryRequestItem{
		ReceivedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		LatencyMS:  42,
		Status:     200,
		Guest:      &api.DebugGuestExecutionEvidence{Runtime: "node22", DurationMS: 17, Outcome: "ok"},
	}
	got, err := (&server{}).buildDebugRequestTimeline(context.Background(), "app", request, nil)
	if err != nil {
		t.Fatalf("buildDebugRequestTimeline: %v", err)
	}
	if len(got) != 3 || got[1].Kind != "guest.execution" || got[1].DurationMS != 17 {
		t.Fatalf("guest timeline = %+v", got)
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
	if got.Stages[0].Status != "partial" || !got.Stages[0].Approximate || got.Stages[0].DurationMS != 12 {
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

func TestBuildDebugRequestCorrelationMarksEstimatedGuestExecutionPartial(t *testing.T) {
	request := api.DebugTelemetryRequestItem{
		Guest: &api.DebugGuestExecutionEvidence{Runtime: "python313", DurationMS: 83, Outcome: "handler_error", ErrorClass: "handler_exec"},
	}
	timeline := []api.DebugTimelineEvent{{
		At: "2026-09-09T12:00:00.200Z", Phase: "guest", Kind: "guest.execution",
		DurationMS: 83, Status: 500, Approximate: true,
	}}
	got := buildDebugRequestCorrelation(request, timeline, nil)
	stage := got.Stages[3]
	if stage.Status != "partial" || stage.DurationMS != 83 || stage.EvidenceCount != 1 || !stage.Approximate {
		t.Fatalf("guest stage = %+v", stage)
	}
	if !strings.Contains(stage.Reason, "python313") || !strings.Contains(stage.Reason, "handler_exec") {
		t.Fatalf("guest stage reason = %q", stage.Reason)
	}
}
