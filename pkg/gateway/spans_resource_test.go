package gateway

// Retained spans keep the allowlisted OTLP resource attributes (service
// name, version, instance, environment, SDK language), so a span in a
// cross-service trace says which service produced it. They count toward
// the attribute cap, take priority within it, and never override the
// span's own attributes; other resource attributes are not retained.

import (
	"fmt"
	"testing"

	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func resourceExport(resource []*commonpb.KeyValue, spans ...*tracepb.Span) *collectortracepb.ExportTraceServiceRequest {
	return &collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{
		Resource:   &resourcepb.Resource{Attributes: resource},
		ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}},
	}}}
}

func TestExtractTraceBatchesKeepsResourceIdentity(t *testing.T) {
	req := resourceExport([]*commonpb.KeyValue{
		stringAttr("service.name", "checkout"),
		stringAttr("service.version", "v80"),
		stringAttr("deployment.environment.name", "production"),
		stringAttr("telemetry.sdk.language", "python"),
		stringAttr("host.name", "ip-10-0-0-2"),
		stringAttr("process.command_line", "python app.py --token=secret"),
	}, &tracepb.Span{
		TraceId: traceID(1), SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 8}, Name: "GET /orders",
		Attributes: []*commonpb.KeyValue{stringAttr("http.route", "/orders"), stringAttr("service.version", "span-wins")},
	})
	batches, err := extractTraceBatches(req)
	if err != nil {
		t.Fatal(err)
	}
	attrs := batches[0].spans[0].Attributes
	want := map[string]string{
		"service.name":                "checkout",
		"service.version":             "span-wins",
		"deployment.environment.name": "production",
		"telemetry.sdk.language":      "python",
		"http.route":                  "/orders",
	}
	for key, value := range want {
		if attrs[key] != value {
			t.Errorf("%s = %q, want %q", key, attrs[key], value)
		}
	}
	for _, key := range []string{"host.name", "process.command_line"} {
		if _, ok := attrs[key]; ok {
			t.Errorf("non-allowlisted resource attribute %s retained", key)
		}
	}
	if batches[0].spans[0].resource != nil {
		t.Error("resource map left on the summarized span")
	}
}

func TestResourceIdentitySurvivesAttributeCap(t *testing.T) {
	var spanAttrs []*commonpb.KeyValue
	for i := 0; i < 2*spanMaxAttributes; i++ {
		spanAttrs = append(spanAttrs, stringAttr(fmt.Sprintf("a.%02d", i), "v"))
	}
	req := resourceExport([]*commonpb.KeyValue{stringAttr("service.name", "checkout"), stringAttr("service.instance.id", "vm-1")},
		&tracepb.Span{TraceId: traceID(2), SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 9}, Name: "work", Attributes: spanAttrs})
	batches, err := extractTraceBatches(req)
	if err != nil {
		t.Fatal(err)
	}
	attrs := batches[0].spans[0].Attributes
	if len(attrs) != spanMaxAttributes {
		t.Fatalf("%d attributes, want the cap %d", len(attrs), spanMaxAttributes)
	}
	if attrs["service.name"] != "checkout" || attrs["service.instance.id"] != "vm-1" {
		t.Fatalf("resource identity dropped under the cap: %v", attrs)
	}
}

func TestSpansWithoutResourceUnchanged(t *testing.T) {
	req := resourceExport(nil, &tracepb.Span{TraceId: traceID(3), SpanId: []byte{1, 2, 3, 4, 5, 6, 7, 10}, Name: "plain"})
	batches, err := extractTraceBatches(req)
	if err != nil {
		t.Fatal(err)
	}
	if attrs := batches[0].spans[0].Attributes; len(attrs) != 0 {
		t.Fatalf("span without attributes or resource got %v", attrs)
	}
}
