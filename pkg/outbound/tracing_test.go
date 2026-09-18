package outbound

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestHandlerAddsAutomaticDependencySpans(t *testing.T) {
	prevProvider := otel.GetTracerProvider()
	prevPropagator := otel.GetTextMapPropagator()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(prevProvider)
		otel.SetTextMapPropagator(prevPropagator)
	})

	var upstreamTraceparent string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamTraceparent = r.Header.Get("traceparent")
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	integration := testIntegration(t, server.URL, "secret", []string{"app-1"}, 100, 1, 1)
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(resolver, NewMemoryBackend(), server.Client())
	if err != nil {
		t.Fatal(err)
	}

	rootCtx, root := provider.Tracer("test").Start(context.Background(), "gateway.handler")
	req := gatewayRequest(Prefix+integration.ID+"/v1/items?api_key=secret", "secret", "app-1", http.MethodGet, nil).WithContext(rootCtx)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	root.End()

	if rr.Code != http.StatusOK {
		t.Fatalf("handler status = %d, body=%s", rr.Code, rr.Body.String())
	}
	if upstreamTraceparent == "" {
		t.Fatal("upstream request did not receive traceparent")
	}
	if sc := (propagation.TraceContext{}).Extract(context.Background(), propagation.HeaderCarrier(http.Header{"Traceparent": []string{upstreamTraceparent}})); !oteltrace.SpanContextFromContext(sc).IsValid() {
		t.Fatalf("upstream traceparent is invalid: %q", upstreamTraceparent)
	}

	var binding, client sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case "gregale.outbound.integration":
			binding = span
		case "HTTP GET":
			client = span
		}
	}
	if binding == nil || client == nil {
		t.Fatalf("spans = %#v, want binding and HTTP client spans", recorder.Ended())
	}
	if client.Parent().SpanID() != binding.SpanContext().SpanID() {
		t.Fatalf("client parent = %s, want binding span %s", client.Parent().SpanID(), binding.SpanContext().SpanID())
	}
	if client.SpanKind() != oteltrace.SpanKindClient {
		t.Fatalf("client span kind = %s, want client", client.SpanKind())
	}

	attrs := spanAttributes(binding)
	if attrs[integrationIDKey] != integration.ID || attrs[appIDKey] != "app-1" || attrs[originHostKey] == "" {
		t.Fatalf("binding attributes = %#v, want bounded integration metadata", attrs)
	}
	for _, span := range recorder.Ended() {
		for _, attr := range span.Attributes() {
			if strings.Contains(attr.Value.AsString(), "api_key") || strings.Contains(attr.Value.AsString(), "secret") {
				t.Fatalf("span %q leaked request secret in attribute %s=%q", span.Name(), attr.Key, attr.Value.AsString())
			}
		}
	}
}

func spanAttributes(span sdktrace.ReadOnlySpan) map[string]string {
	attrs := make(map[string]string)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	return attrs
}
