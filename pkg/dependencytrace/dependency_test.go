package dependencytrace_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/dependencytrace"
)

func TestDependencyTransportCreatesSafeClientSpan(t *testing.T) {
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

	var receivedTraceparent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedTraceparent = r.Header.Get("traceparent")
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	client := &http.Client{Transport: dependencytrace.NewDependencyTransport(server.Client().Transport,
		attribute.String("gregale.dependency.type", "managed_binding"),
		attribute.String("gregale.binding.type", "object_storage"),
		attribute.String("gregale.binding.provider", "s3"),
	)}
	rootCtx, root := provider.Tracer("test").Start(context.Background(), "request")
	request, err := http.NewRequestWithContext(rootCtx, http.MethodPost, server.URL+"/objects?token=secret", strings.NewReader("body-secret"))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	root.End()

	if receivedTraceparent == "" {
		t.Fatal("dependency request did not receive traceparent")
	}
	if spanContext := (propagation.TraceContext{}).Extract(context.Background(), propagation.HeaderCarrier(http.Header{"Traceparent": []string{receivedTraceparent}})); !oteltrace.SpanContextFromContext(spanContext).IsValid() {
		t.Fatalf("dependency traceparent is invalid: %q", receivedTraceparent)
	}

	var clientSpan sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		if span.Name() == "HTTP POST" {
			clientSpan = span
			break
		}
	}
	if clientSpan == nil {
		t.Fatalf("ended spans = %#v, want HTTP client span", recorder.Ended())
	}
	if clientSpan.SpanKind() != oteltrace.SpanKindClient {
		t.Fatalf("span kind = %s, want client", clientSpan.SpanKind())
	}
	if clientSpan.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Fatalf("client parent = %s, want root %s", clientSpan.Parent().SpanID(), root.SpanContext().SpanID())
	}
	for _, attr := range clientSpan.Attributes() {
		value := attr.Value.AsString()
		if strings.Contains(value, "secret") || strings.Contains(value, "/objects") {
			t.Fatalf("span leaked request data in %s=%q", attr.Key, value)
		}
	}
}
