package issues

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	logscollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func exceptionAttrs() []*commonpb.KeyValue {
	return []*commonpb.KeyValue{{Key: "exception.type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "DateFormatError"}}}, {Key: "exception.message", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "failure for alice@example.com"}}}}
}

// adr: 1111
func TestIssueOTLPGroupingControlsAcrossSignals(t *testing.T) {
	now := time.Now().UTC()
	values := map[string]string{
		"exception.type": "Error", "exception.message": "failure",
		"exception.stacktrace":               "Error: failure\n    at charge (/app/checkout.ts:10:5)",
		"gregale.issue.grouping_strategy":    FailureSiteStrategy,
		"gregale.issue.error_code":           "PAYMENT_404",
		"gregale.issue.frame_order":          "newest-first",
		"gregale.issue.fingerprint_override": "payments",
	}
	attrs := []*commonpb.KeyValue{}
	for key, value := range values {
		attrs = append(attrs, &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}})
	}
	stamp := uint64(now.UnixNano())
	traces, err := protojson.Marshal(&tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Events: []*tracepb.Span_Event{{Name: "exception", TimeUnixNano: stamp, Attributes: attrs}}}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	logs, err := protojson.Marshal(&logscollector.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{TimeUnixNano: stamp, Attributes: attrs}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	expected := api.IssueEvent{
		EventID: "a1111111-1111-4111-8111-111111111111", OccurredAt: now,
		ExceptionType: "Error", GroupingStrategy: FailureSiteStrategy,
		FingerprintOverride: "payments",
	}
	_, want, _, err := Normalize(expected, now, api.PlanHobby.IssueLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		signal string
		raw    []byte
	}{{"traces", traces}, {"logs", logs}} {
		t.Run(tc.signal, func(t *testing.T) {
			events, err := ExtractOTLP(tc.raw, tc.signal)
			if err != nil || len(events) != 1 {
				t.Fatalf("extract: %+v %v", events, err)
			}
			event := events[0]
			if event.GroupingStrategy != FailureSiteStrategy || event.FrameOrder != "newest-first" || event.ErrorCode != "PAYMENT_404" || event.FingerprintOverride != "payments" {
				t.Fatalf("controls lost: %+v", event)
			}
			retry, err := ExtractOTLP(tc.raw, tc.signal)
			if err != nil || retry[0].EventID != event.EventID {
				t.Fatal("grouping controls destabilized retry ID")
			}
			out, fp, _, err := Normalize(event, now, api.PlanHobby.IssueLimits())
			if err != nil || fp != want || GroupingVersionFor(out) != 2 {
				t.Fatalf("OTLP override/version mismatch: %s %s %v", fp, want, err)
			}
		})
	}
}

func TestIssueOTLPIndependentExceptionDelivery(t *testing.T) {
	now := time.Now().UTC()
	stamp := uint64(now.UnixNano())
	trace := bytes.Repeat([]byte{1}, 16)
	span := bytes.Repeat([]byte{2}, 8)
	traces, _ := protojson.Marshal(&tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{TraceId: trace, SpanId: span, Events: []*tracepb.Span_Event{{Name: "exception", TimeUnixNano: stamp, Attributes: exceptionAttrs()}}}}}}}}})
	logs, _ := protojson.Marshal(&logscollector.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{{TimeUnixNano: stamp, Attributes: exceptionAttrs()}}}}}}})
	for _, tc := range []struct {
		signal  string
		payload []byte
		traced  bool
	}{{"traces", traces, true}, {"logs", logs, false}} {
		t.Run(tc.signal, func(t *testing.T) {
			first, err := ExtractOTLP(tc.payload, tc.signal)
			if err != nil || len(first) != 1 {
				t.Fatalf("extract: %+v %v", first, err)
			}
			retry, _ := ExtractOTLP(tc.payload, tc.signal)
			if first[0].EventID != retry[0].EventID {
				t.Fatal("exporter retry changed event identity")
			}
			normalized, _, _, err := Normalize(first[0], now, api.PlanHobby.IssueLimits())
			if err != nil || len(normalized.Redactions) == 0 {
				t.Fatalf("unsampled exception or redaction lost: %+v %v", normalized, err)
			}
			if (normalized.TraceID != "") != tc.traced {
				t.Fatal("fabricated or lost trace correlation")
			}
		})
	}
}

func TestIssueOTLPIdenticalOccurrencesHaveStableDistinctIDs(t *testing.T) {
	stamp := uint64(time.Now().UnixNano())
	record := &logspb.LogRecord{TimeUnixNano: stamp, Attributes: exceptionAttrs()}
	raw, err := protojson.Marshal(&logscollector.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{record, record}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := ExtractOTLP(raw, "logs")
	if err != nil || len(first) != 2 || first[0].EventID == first[1].EventID {
		t.Fatalf("identical records must have distinct IDs: %+v %v", first, err)
	}
	retry, err := ExtractOTLP(raw, "logs")
	if err != nil || len(retry) != 2 || retry[0].EventID != first[0].EventID || retry[1].EventID != first[1].EventID {
		t.Fatalf("export retry changed IDs: %+v %v", retry, err)
	}
}

func TestIssueOTLPRejectsMalformedAndOversizedBatches(t *testing.T) {
	for _, tc := range []struct{ signal, body string }{{"unknown", `{}`}, {"logs", `{invalid`}, {"logs", `{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}`}} {
		if _, err := ExtractOTLP([]byte(tc.body), tc.signal); err == nil {
			t.Fatalf("accepted invalid export %s", tc.body)
		}
	}
	items := make([]*logspb.LogRecord, api.IssueMaxBatchEvents+1)
	for i := range items {
		items[i] = &logspb.LogRecord{TimeUnixNano: uint64(time.Now().UnixNano()), Attributes: exceptionAttrs()}
	}
	raw, _ := protojson.Marshal(&logscollector.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: items}}}}})
	if _, err := ExtractOTLP(raw, "logs"); err == nil {
		t.Fatal("accepted unbounded batch")
	}
}

func TestIssueOTLPGroupsKeepInvalidRecordsSeparate(t *testing.T) {
	stamp := uint64(time.Now().UnixNano())
	good := &logspb.LogRecord{TimeUnixNano: stamp, Attributes: exceptionAttrs()}
	bad := &logspb.LogRecord{Attributes: exceptionAttrs()}
	raw, _ := protojson.Marshal(&logscollector.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: []*logspb.LogRecord{good, bad}}}}}})
	groups, err := ExtractOTLPGroups(raw, "logs")
	if err != nil || len(groups) != 2 || len(groups[0].Events) != 1 || groups[0].Err != nil || groups[1].Err == nil {
		t.Fatalf("log groups: %+v %v", groups, err)
	}
	span := &tracepb.Span{TraceId: bytes.Repeat([]byte{1}, 16), SpanId: bytes.Repeat([]byte{2}, 8), Events: []*tracepb.Span_Event{{Name: "exception", TimeUnixNano: stamp, Attributes: exceptionAttrs()}, {Name: "exception", Attributes: exceptionAttrs()}}}
	raw, _ = protojson.Marshal(&tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{span}}}}}})
	groups, err = ExtractOTLPGroups(raw, "traces")
	if err != nil || len(groups) != 1 || len(groups[0].Events) != 1 || groups[0].Err == nil {
		t.Fatalf("span group: %+v %v", groups, err)
	}
}

func TestIssueOTLPNumericNanosecondsPreserveIdentity(t *testing.T) {
	now := time.Now().UTC()
	var ids []string
	for _, nanos := range []int64{now.UnixNano(), now.UnixNano() + 1} {
		raw := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":%d,"attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}`, nanos)
		events, err := ExtractOTLP([]byte(raw), "logs")
		if err != nil || len(events) != 1 || events[0].OccurredAt.UnixNano() != nanos {
			t.Fatalf("timestamp %d: %+v %v", nanos, events, err)
		}
		ids = append(ids, events[0].EventID)
	}
	if ids[0] == ids[1] {
		t.Fatal("distinct timestamps collided")
	}
}

func TestIssueOTLPMalformedIDIsIsolated(t *testing.T) {
	now := time.Now().UTC()
	raw := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"traceId":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},{"timeUnixNano":"%d","traceId":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz","attributes":[{"key":"exception.type","value":{"stringValue":"Bad"}}]},{"timeUnixNano":"%d","attributes":[{"key":"exception.type","value":{"stringValue":"Good"}}]}]}]}]}`, now.UnixNano(), now.UnixNano())
	groups, err := ExtractOTLPGroups([]byte(raw), "logs")
	if err != nil || len(groups) != 2 {
		t.Fatalf("groups: %+v %v", groups, err)
	}
	if _, _, _, err = Normalize(groups[0].Events[0], now, api.PlanHobby.IssueLimits()); err != nil || groups[0].Events[0].TraceID != "" {
		t.Fatalf("invalid optional log ID should be absent: %v", err)
	}
	if _, _, _, err = Normalize(groups[1].Events[0], now, api.PlanHobby.IssueLimits()); err != nil {
		t.Fatalf("valid record rejected: %v", err)
	}
	trace := fmt.Sprintf(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"invalid-base64","events":[{"name":"exception","timeUnixNano":"%d","attributes":[{"key":"exception.type","value":{"stringValue":"Bad"}}]}]},{"traceId":"11111111111111111111111111111111","spanId":"2222222222222222","events":[{"name":"exception","timeUnixNano":"%d","attributes":[{"key":"exception.type","value":{"stringValue":"Good"}}]}]}]}]}]}`, now.UnixNano(), now.UnixNano())
	groups, err = ExtractOTLPGroups([]byte(trace), "traces")
	if err != nil || len(groups) != 2 {
		t.Fatalf("trace groups: %+v %v", groups, err)
	}
	if groups[0].Err == nil {
		t.Fatal("invalid trace span IDs were accepted")
	}
	if _, _, _, err = Normalize(groups[1].Events[0], now, api.PlanHobby.IssueLimits()); err != nil {
		t.Fatalf("valid span rejected: %v", err)
	}
}

func TestIssueOTLPTraceRequiresBothIDs(t *testing.T) {
	now := time.Now().UTC()
	for _, ids := range []struct{ trace, span string }{
		{"", ""}, {strings.Repeat("1", 32), ""}, {"", strings.Repeat("2", 16)},
		{strings.Repeat("0", 32), strings.Repeat("2", 16)}, {strings.Repeat("1", 32), strings.Repeat("0", 16)},
	} {
		raw := fmt.Sprintf(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":%q,"spanId":%q,"events":[{"name":"exception","timeUnixNano":"%d","attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}]}`, ids.trace, ids.span, now.UnixNano())
		groups, err := ExtractOTLPGroups([]byte(raw), "traces")
		if err != nil || len(groups) != 1 || groups[0].Err == nil {
			t.Fatalf("invalid trace identifiers accepted: %+v %v", groups, err)
		}
	}
}

func TestIssueOTLPLogInvalidCorrelationIsAbsent(t *testing.T) {
	now := time.Now().UTC()
	for _, ids := range []struct{ trace, span string }{
		{strings.Repeat("0", 32), strings.Repeat("0", 16)},
		{"bad", "not-base64"},
	} {
		raw := fmt.Sprintf(`{"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","traceId":%q,"spanId":%q,"attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}`, now.UnixNano(), ids.trace, ids.span)
		events, err := ExtractOTLP([]byte(raw), "logs")
		if err != nil || len(events) != 1 || events[0].TraceID != "" || events[0].SpanID != "" {
			t.Fatalf("log correlation: %+v %v", events, err)
		}
		if _, _, _, err := Normalize(events[0], now, api.PlanHobby.IssueLimits()); err != nil {
			t.Fatalf("exception rejected: %v", err)
		}
	}
}

func TestIssueOTLPProtoJSONFieldNamesAndHexIDs(t *testing.T) {
	now := time.Now().UTC()
	raw := fmt.Sprintf(`{"resource_logs":[{"scope_logs":[{"log_records":[{"time_unix_nano":"%d","trace_id":"abababababababababababababababab","span_id":"cdcdcdcdcdcdcdcd","attributes":[{"key":"exception.type","value":{"string_value":"Error"}}]}]}]}]}`, now.UnixNano())
	events, err := ExtractOTLP([]byte(raw), "logs")
	if err != nil || len(events) != 1 || events[0].TraceID != "abababababababababababababababab" || events[0].SpanID != "cdcdcdcdcdcdcdcd" {
		t.Fatalf("protobuf field names: %+v %v", events, err)
	}
}

func TestIssueOTLPHexIDsAndUnknownExtensions(t *testing.T) {
	stamp := time.Now().UnixNano()
	raw := fmt.Sprintf(`{"unknown":{"traceId":"not-a-trace"},"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","traceId":"ABABABABABABABABABABABABABABABAB","spanId":"CDCDCDCDCDCDCDCD","futureField":true,"attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}`, stamp)
	events, err := ExtractOTLP([]byte(raw), "logs")
	if err != nil || len(events) != 1 || events[0].TraceID != "abababababababababababababababab" || events[0].SpanID != "cdcdcdcdcdcdcdcd" {
		t.Fatalf("OTLP encoding mismatch: %+v %v", events, err)
	}
}
