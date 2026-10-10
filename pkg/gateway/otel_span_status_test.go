package gateway

import (
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func stringKV(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func TestSummarizeSpanNormalizesStatusAndErrorType(t *testing.T) {
	exception := &tracepb.Span_Event{Name: "exception", Attributes: []*commonpb.KeyValue{
		stringKV("exception.type", "QueryTimeout"),
		stringKV("exception.message", "timeout for user alice@example.com"),
		stringKV("exception.stacktrace", "at db.query()"),
	}}
	for name, tc := range map[string]struct {
		span          *tracepb.Span
		wantStatus    string
		wantErrorType string
	}{
		"unset": {span: &tracepb.Span{}, wantStatus: ""},
		"ok":    {span: &tracepb.Span{Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}}, wantStatus: "ok"},
		"error with error.type": {
			span:       &tracepb.Span{Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}, Attributes: []*commonpb.KeyValue{stringKV("error.type", "503")}, Events: []*tracepb.Span_Event{exception}},
			wantStatus: "error", wantErrorType: "503",
		},
		"error with exception event": {
			span:       &tracepb.Span{Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR}, Events: []*tracepb.Span_Event{{Name: "retry"}, exception}},
			wantStatus: "error", wantErrorType: "QueryTimeout",
		},
		"exception on a successful span is not a failure": {
			span:       &tracepb.Span{Status: &tracepb.Status{Code: tracepb.Status_STATUS_CODE_OK}, Events: []*tracepb.Span_Event{exception}},
			wantStatus: "ok",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := summarizeSpan(tc.span)
			if got.Status != tc.wantStatus || got.ErrorType != tc.wantErrorType {
				t.Fatalf("status=%q error_type=%q, want %q %q", got.Status, got.ErrorType, tc.wantStatus, tc.wantErrorType)
			}
			for key := range got.Attributes {
				if key == "exception.message" || key == "exception.stacktrace" {
					t.Fatalf("exception event attribute %q leaked into span attributes", key)
				}
			}
		})
	}
}
