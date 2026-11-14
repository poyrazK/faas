package otelinit_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// With the head ratio at 0 nothing is sampled, except dependency spans: they
// are recorded at the root and under a request span the ratio dropped, which
// is where a service-proxy span lands when the calling app forwards the
// gateway's unsampled traceparent.
func TestDependencySpansAreRecordedWhateverTheRatio(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(otelinit.NewDependencyAlways(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0)))),
		sdktrace.WithSpanProcessor(rec),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()
	tr := tp.Tracer("test")
	dep := oteltrace.WithAttributes(
		attribute.String(otelinit.DependencySpanAttribute, "managed_binding"),
		attribute.String("gregale.dependency.kind", "service_proxy"),
	)

	ctx, request := tr.Start(context.Background(), "request")
	if request.SpanContext().IsSampled() || request.IsRecording() {
		t.Fatal("request span was recorded at ratio 0")
	}
	if !request.SpanContext().IsValid() {
		t.Fatal("unsampled request span has no valid trace id; the trace id header needs one")
	}
	_, child := tr.Start(ctx, "inner")
	_, under := tr.Start(ctx, "service call", dep)
	_, root := tr.Start(context.Background(), "outbound call", dep)
	if child.IsRecording() {
		t.Error("an ordinary child of a dropped request span was recorded")
	}
	for name, s := range map[string]oteltrace.Span{"dependency under a dropped parent": under, "dependency root": root} {
		if !s.IsRecording() || !s.SpanContext().IsSampled() {
			t.Errorf("%s was not recorded", name)
		}
	}
	if under.SpanContext().TraceID() != request.SpanContext().TraceID() {
		t.Error("dependency span left the request's trace")
	}
	for _, s := range []oteltrace.Span{child, under, root, request} {
		s.End()
	}
	if got := len(rec.Ended()); got != 2 {
		t.Fatalf("recorded %d spans, want the 2 dependency spans", got)
	}
}
