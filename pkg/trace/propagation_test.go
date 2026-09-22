package trace_test

import (
	"context"
	"encoding/json"
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

func TestMergeHeadersPlatformContextOverridesCallerValues(t *testing.T) {
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
		TraceID: traceID, SpanID: spanID, TraceFlags: oteltrace.FlagsSampled,
	}))

	raw, err := pkgtrace.MergeHeaders(ctx, json.RawMessage(`{"X-Request-ID":"caller","TraceParent":"00-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-bbbbbbbbbbbbbbbb-01","x-gregale-trace-id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","x-app":"keep"}`))
	if err != nil {
		t.Fatalf("MergeHeaders: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["x-app"] != "keep" || got["X-Request-ID"] != "caller" {
		t.Fatalf("caller headers not preserved: %+v", got)
	}
	if got["X-Gregale-Trace-Id"] != traceID.String() {
		t.Fatalf("canonical trace id = %q, want %q", got["X-Gregale-Trace-Id"], traceID)
	}
	if got["traceparent"] != "00-"+traceID.String()+"-"+spanID.String()+"-01" {
		t.Fatalf("traceparent = %q", got["traceparent"])
	}
	for key := range got {
		if key == "TraceParent" || key == "x-gregale-trace-id" {
			t.Fatalf("caller propagation key survived under spelling %q", key)
		}
	}
}

func TestPropagateHeadersRestoresProducerContext(t *testing.T) {
	previous := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	defer otel.SetTextMapPropagator(previous)
	traceID, err := oteltrace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	spanID, err := oteltrace.SpanIDFromHex("bbbbbbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	producer := oteltrace.ContextWithSpanContext(context.Background(), oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: oteltrace.FlagsSampled,
	}))
	incoming := pkgtrace.InjectHeaders(producer)
	propagated := pkgtrace.PropagateHeaders(context.Background(), incoming)
	if got := propagated["traceparent"]; got != incoming["traceparent"] {
		t.Fatalf("traceparent = %q, want %q", got, incoming["traceparent"])
	}
	if got := propagated["X-Gregale-Trace-Id"]; got != traceID.String() {
		t.Fatalf("trace id = %q, want %q", got, traceID)
	}
}
