package trace_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	pkgtrace "github.com/onebox-faas/faas/pkg/trace"
)

// No server span carries a request path: inbound webhook and hosted MCP
// upload URLs hold a working credential in the path, and spans leave the host
// once an OTLP endpoint is set (bug hunt 2026-10-28 H100). The span records
// the matched route pattern instead.
func TestHTTPHandlerRecordsTheRoutePatternNotThePath(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/hooks/{token}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	h := pkgtrace.HTTPHandler("test", mux)
	const secret = "whk_live_3b1f0c9e"
	for _, path := range []string{"/v1/hooks/" + secret, "/mcp/uploads/" + secret} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
	}

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("ended spans = %d, want 2", len(spans))
	}
	want := []string{"/v1/hooks/{token}", "unmatched"}
	for i, span := range spans {
		if strings.Contains(span.Name(), secret) {
			t.Fatalf("span name %q carries the token", span.Name())
		}
		var path string
		for _, kv := range span.Attributes() {
			if strings.Contains(kv.Value.Emit(), secret) {
				t.Fatalf("span attribute %s = %q carries the token", kv.Key, kv.Value.Emit())
			}
			if kv.Key == "url.path" {
				path = kv.Value.AsString()
			}
		}
		if path != want[i] {
			t.Fatalf("span %d url.path = %q, want %q", i, path, want[i])
		}
	}
}
