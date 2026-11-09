package gateway

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func stringAttr(key, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

// A customer span is bounded before it is buffered: spans were stored at
// full size (any attribute, the status message, the DB statement twice), so
// one account could write megabytes per trace into shared telemetry rows.
func TestSummarizeSpanBoundsCustomerData(t *testing.T) {
	attrs := []*commonpb.KeyValue{
		stringAttr("db.statement", "SELECT * FROM users WHERE email = 'alice@example.com' AND id = 42"),
		stringAttr(strings.Repeat("k", 500), "v"),
		stringAttr("http.url", strings.Repeat("é", 400)),
	}
	for i := 0; i < 60; i++ {
		attrs = append(attrs, stringAttr(fmt.Sprintf("extra.%02d", i), strings.Repeat("x", 4096)))
	}
	span := summarizeSpan(&tracepb.Span{
		TraceId: traceID(1), SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8},
		Name:       strings.Repeat("n", 10_000),
		Attributes: attrs,
		Status:     &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "panic: card 4242424242424242 declined"},
	})
	if span.StatusMessage != "" {
		t.Errorf("status message kept: %q", span.StatusMessage)
	}
	if len(span.Name) > spanMaxNameBytes {
		t.Errorf("name is %d bytes", len(span.Name))
	}
	if strings.Contains(span.DBStatement, "alice") || strings.Contains(span.DBStatement, "42") {
		t.Errorf("statement kept literals: %q", span.DBStatement)
	}
	if _, ok := span.Attributes["db.statement"]; ok {
		t.Error("statement stored twice (attribute and DBStatement)")
	}
	if len(span.Attributes) > spanMaxAttributes {
		t.Errorf("%d attributes, want at most %d", len(span.Attributes), spanMaxAttributes)
	}
	for key, value := range span.Attributes {
		if len(key) > spanMaxAttrKeyBytes || len(value) > spanMaxAttrValueBytes {
			t.Errorf("attribute %d/%d bytes", len(key), len(value))
		}
		if !utf8.ValidString(key) || !utf8.ValidString(value) {
			t.Errorf("attribute cut inside a rune: %q", value)
		}
	}
}

// Only the attributes the debugger reads are stored, a URL keeps only its
// origin, and a Redis command keeps only its verb.
func TestSummarizeSpanStoresOnlyDependencyAttributes(t *testing.T) {
	span := summarizeSpan(&tracepb.Span{
		TraceId: traceID(1), SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8}, Name: "GET",
		Attributes: []*commonpb.KeyValue{
			stringAttr("http.request.method", "GET"),
			stringAttr("url.full", "https://user:pw@api.stripe.com/v1/customers/cus_123?token=secret"),
			stringAttr("enduser.id", "alice@example.com"),
			stringAttr("http.request.header.authorization", "Bearer abc"),
			stringAttr("app.cart_id", "cart-9"),
			stringAttr("http.response.status_code", "200"),
		},
	})
	want := map[string]string{"http.request.method": "GET", "url.full": "https://api.stripe.com", "http.response.status_code": "200"}
	if len(span.Attributes) != len(want) {
		t.Fatalf("stored attributes = %v, want only %v", span.Attributes, want)
	}
	for key, value := range want {
		if span.Attributes[key] != value {
			t.Errorf("%s = %q, want %q", key, span.Attributes[key], value)
		}
	}

	redis := summarizeSpan(&tracepb.Span{
		TraceId: traceID(1), SpanId: []byte{2, 2, 3, 4, 5, 6, 7, 8}, Name: "SET",
		Attributes: []*commonpb.KeyValue{stringAttr("db.system", "redis"), stringAttr("db.statement", "SET session:abc123 eyJhbGciOi")},
	})
	if redis.DBStatement != "SET ? ?" {
		t.Errorf("redis statement = %q, want the command only", redis.DBStatement)
	}
}
