// adr: 127

package sched

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestTriggerDispatchParentExtractsBrokerContextAndLinksBatch(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	rootCtx, root := provider.Tracer("test").Start(context.Background(), "source")
	header := make(map[string]string)
	otel.GetTextMapPropagator().Inject(rootCtx, triggerHeaderCarrier(header))
	root.End()

	parent, links := triggerDispatchParent(context.Background(), []SourceRecord{
		{Headers: map[string]string{"TraceParent": header["traceparent"]}},
	})
	if got := oteltrace.SpanContextFromContext(parent); !got.IsValid() {
		t.Fatal("extracted parent is invalid")
	} else if got.TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("parent trace id = %s, want %s", got.TraceID(), root.SpanContext().TraceID())
	}
	if len(links) != 1 || links[0].SpanContext.TraceID() != root.SpanContext().TraceID() {
		t.Fatalf("links = %+v, want one link to source trace", links)
	}
}
