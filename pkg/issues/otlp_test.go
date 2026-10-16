package issues

import (
	"bytes"
	"fmt"
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
	traces, err := protojson.Marshal(&tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Events: []*tracepb.Span_Event{{Name: "exception", TimeUnixNano: stamp, Attributes: attrs}}}}}}}}})
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

func TestIssueOTLPHexIDsAndUnknownExtensions(t *testing.T) {
	stamp := time.Now().UnixNano()
	raw := fmt.Sprintf(`{"unknown":{"traceId":"not-a-trace"},"resourceLogs":[{"scopeLogs":[{"logRecords":[{"timeUnixNano":"%d","traceId":"ABABABABABABABABABABABABABABABAB","spanId":"CDCDCDCDCDCDCDCD","futureField":true,"attributes":[{"key":"exception.type","value":{"stringValue":"Error"}}]}]}]}]}`, stamp)
	events, err := ExtractOTLP([]byte(raw), "logs")
	if err != nil || len(events) != 1 || events[0].TraceID != "abababababababababababababababab" || events[0].SpanID != "cdcdcdcdcdcdcdcd" {
		t.Fatalf("OTLP encoding mismatch: %+v %v", events, err)
	}
}
