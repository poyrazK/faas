package trace_test

import (
	"context"
	"testing"

	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestQueuePropagationPreservesW3CContextAndCanonicalID(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(previous)
	traceID, err := oteltrace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := oteltrace.SpanIDFromHex("00f067aa0ba902b7")
	if err != nil {
		t.Fatal(err)
	}
	ctx := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: oteltrace.FlagsSampled,
	}))

	headers := pkgtrace.InjectHeaders(ctx)
	if got := headers["X-Gregale-Trace-Id"]; got != traceID.String() {
		t.Fatalf("canonical trace id = %q, want %q", got, traceID.String())
	}
	if got := headers["traceparent"]; got != "00-"+traceID.String()+"-"+spanID.String()+"-01" {
		t.Fatalf("traceparent = %q", got)
	}
	restored := pkgtrace.ExtractHeaders(context.Background(), headers)
	if got := pkgtrace.SpanFromContext(restored).SpanContext().TraceID(); got != traceID {
		t.Fatalf("restored trace id = %s, want %s", got, traceID)
	}
}
