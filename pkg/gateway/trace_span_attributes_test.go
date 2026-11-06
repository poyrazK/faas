package gateway

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var (
	spanAttributesIdentity = api.PlatformIdentity{RequestID: "req-1", AppID: "app-1", DeploymentID: "dep-a", InstanceID: "i-a", NodeID: "node-1"}
	spanAttributesTarget   = Target{DeploymentID: "dep-b", InstanceID: "i-b", NodeID: "node-2"}
	spanAttributesDecision = trafficDecisionSnapshot{path: "forward", outcome: "served", attempts: 2, policyStage: "pinned"}
)

// The request span's attributes as separate SetAttributes calls, the way
// ServeHTTP wrote them before they were collected.
func setRequestSpanAttributesSeparately(span oteltrace.Span) {
	span.SetAttributes(attribute.String("app_id", "app-1"), attribute.String("app_plan", "pro"))
	span.SetAttributes(pkgtrace.PlatformIdentityAttributes(spanAttributesIdentity)...)
	span.SetAttributes(completionTargetAttributes(spanAttributesTarget)...)
	span.SetAttributes(spanAttributesDecision.attributes()...)
	span.SetAttributes(attribute.Int("http.status_code", 200))
}

func setRequestSpanAttributesCollected(span oteltrace.Span) {
	collected := collectSpanAttributes(span)
	collected.add(attribute.String("app_id", "app-1"), attribute.String("app_plan", "pro"))
	collected.addIdentity(spanAttributesIdentity)
	collected.addTarget(spanAttributesTarget)
	collected.addDecision(spanAttributesDecision)
	collected.add(attribute.Int("http.status_code", 200))
	collected.flush()
}

// Collected attributes end up exactly as separate calls left them, including
// the last value of a key set twice, and a recycled collector starts empty.
func TestSpanAttributesMatchSeparateCalls(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)).Tracer("test")
	for _, set := range []func(oteltrace.Span){setRequestSpanAttributesSeparately, setRequestSpanAttributesCollected, setRequestSpanAttributesCollected} {
		_, span := tracer.Start(context.Background(), "gateway.request", oteltrace.WithAttributes(attribute.String("http.method", "GET")))
		set(span)
		span.End()
	}
	ended := recorder.Ended()
	want := attribute.NewSet(ended[0].Attributes()...)
	if got, ok := want.Value("deployment_id"); !ok || got.AsString() != "dep-b" {
		t.Fatalf("separate calls left deployment_id %q, want the completion target's dep-b", got.AsString())
	}
	for i, span := range ended[1:] {
		if got := attribute.NewSet(span.Attributes()...); !got.Equals(&want) {
			t.Errorf("collected span %d attributes = %v, want %v", i+1, got.ToSlice(), want.ToSlice())
		}
	}
}

func TestSpanAttributesSkipASpanThatIsNotRecording(t *testing.T) {
	tracer := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())).Tracer("test")
	_, span := tracer.Start(context.Background(), "gateway.request")
	collected := collectSpanAttributes(span)
	if collected != nil {
		t.Fatal("collected attributes for a span that is not recording")
	}
	collected.add(attribute.String("app_id", "app-1"))
	collected.addIdentity(spanAttributesIdentity)
	collected.addTarget(spanAttributesTarget)
	collected.addDecision(spanAttributesDecision)
	collected.flush()
	span.End()
}

func BenchmarkRequestSpanAttributes(b *testing.B) {
	tracer := sdktrace.NewTracerProvider().Tracer("bench")
	for _, bench := range []struct {
		name string
		set  func(oteltrace.Span)
	}{
		{"separate", setRequestSpanAttributesSeparately},
		{"collected", setRequestSpanAttributesCollected},
	} {
		b.Run(bench.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, span := tracer.Start(context.Background(), "gateway.request", oteltrace.WithAttributes(attribute.String("http.method", "GET")))
				bench.set(span)
				span.End()
			}
		})
	}
}
