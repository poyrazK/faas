// adr: 127
package gateway

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestRetainedServiceSpansExporterKeepsOnlyAuthorizedServiceSpans(t *testing.T) {
	accountID := uuid.MustParse("d6e281f3-f5b2-436c-b4ad-8529a956609c")
	acc := NewSpansAccumulator()
	exporter := NewRetainedServiceSpansExporter(acc, nil)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	tracer := provider.Tracer("test")
	rootCtx, root := tracer.Start(context.Background(), "request")
	_, serviceSpan := tracer.Start(rootCtx, "service.orders",
		oteltrace.WithSpanKind(oteltrace.SpanKindClient),
		oteltrace.WithAttributes(
			attribute.String("gregale.dependency.type", "managed_binding"),
			attribute.String("gregale.dependency.kind", "service_proxy"),
			attribute.String(retainedSpanAccountIDAttribute, accountID.String()),
			attribute.String("gregale.service.name", "orders"),
		),
	)
	serviceSpan.SetStatus(codes.Error, "502")
	serviceSpan.End()
	root.End()

	traceID := serviceSpan.SpanContext().TraceID().String()
	spans, gotAccountID := acc.DrainAndRemove(traceID)
	if gotAccountID != accountID {
		t.Fatalf("account id = %s, want %s", gotAccountID, accountID)
	}
	if len(spans) != 1 {
		t.Fatalf("retained spans = %d, want 1", len(spans))
	}
	got := spans[0]
	if got.Name != "service.orders" || got.Kind != "client" || got.Status != "error" || got.StatusMessage != "502" {
		t.Fatalf("retained span = %+v", got)
	}
	if got.ParentSpanID != root.SpanContext().SpanID().String() {
		t.Fatalf("parent span id = %q, want %q", got.ParentSpanID, root.SpanContext().SpanID())
	}
	if got.Attributes["gregale.service.name"] != "orders" {
		t.Fatalf("service name attribute = %q", got.Attributes["gregale.service.name"])
	}
	if _, exposed := got.Attributes[retainedSpanAccountIDAttribute]; exposed {
		t.Fatal("internal account-routing attribute leaked into retained summary")
	}
	if acc.Len() != 0 {
		t.Fatalf("unrelated root span was retained; buckets = %d", acc.Len())
	}
}

func TestRetainedServiceSpansExporterDropsUnattributedSpan(t *testing.T) {
	acc := NewSpansAccumulator()
	exporter := NewRetainedServiceSpansExporter(acc, nil)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	_, span := provider.Tracer("test").Start(context.Background(), "service.orders",
		oteltrace.WithAttributes(
			attribute.String("gregale.dependency.type", "managed_binding"),
			attribute.String("gregale.dependency.kind", "service_proxy"),
		),
	)
	span.End()
	if acc.Len() != 0 {
		t.Fatalf("unattributed span was retained; buckets = %d", acc.Len())
	}
}
