// pkg/trace/middleware.go — HTTP + gRPC interceptors for issue
// #268 / #758 / cluster E commit 16 of the
// platform-observability mega-PR.
//
// Each daemon's HTTP / gRPC server wraps the existing handler
// chain with one of these interceptors so every inbound request
// carries a W3C `traceparent` header (HTTP) or gRPC metadata
// field (gRPC). The trace.SpanFromContext call in the slog
// envelope (cmd/<daemon>/main.go's NewCorrelationLogger) then
// stamps trace_id + span_id on every log line.
//
// The interceptors are pass-through when no TracerProvider is
// configured (the noop path; see pkg/wire/otelinit noop
// fallback). No nil-checks at call sites — otelhttp.NewHandler
// is nil-safe on the noop provider.

package trace

import (
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"

	"github.com/onebox-faas/faas/pkg/api"
)

// HTTPHandler (cluster E commit 16) wraps a net/http.Handler
// with otelhttp so every inbound request gets a span and a
// traceparent extraction per W3C. serviceName is the
// `http.route` attribute + the OTel service.name resource
// (already set via InitTracer). Use this on every cmd/<daemon>/
// main.go's http.Server.Handler.
func HTTPHandler(serviceName string, h http.Handler) http.Handler {
	return otelhttp.NewHandler(withoutRawPath(h), serviceName)
}

// unmatchedPath stands in for the request path on a span whose request no
// route pattern matched.
const unmatchedPath = "unmatched"

// withoutRawPath replaces the url.path otelhttp records on the server span
// with the matched route pattern. Several routes carry a credential in the
// path (inbound webhook URLs /v1/hooks/{token}, hosted MCP uploads
// /mcp/uploads/{token}) and customer apps' paths can hold their own users'
// reset or invite tokens; spans leave the host once an OTLP endpoint is set
// (bug hunt 2026-10-28 H100). The span name is already the pattern.
func withoutRawPath(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := SpanFromContext(r.Context())
		span.SetAttributes(attribute.String("url.path", unmatchedPath))
		defer func() {
			if path := routePath(r.Pattern); path != "" {
				span.SetAttributes(attribute.String("url.path", path))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// routePath is the path part of a ServeMux pattern ("POST host/v1/hooks/{token}"
// gives "/v1/hooks/{token}").
func routePath(pattern string) string {
	if _, rest, ok := strings.Cut(pattern, " "); ok {
		pattern = rest
	}
	if i := strings.Index(pattern, "/"); i >= 0 {
		return pattern[i:]
	}
	return ""
}

// WithTraceIDHeader exposes the canonical OTel trace id on the response.
// It must be placed inside an OTel HTTP handler so the request context already
// carries the server span. Invalid/noop contexts deliberately omit the header.
func WithTraceIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if spanContext := SpanFromContext(r.Context()).SpanContext(); spanContext.IsValid() {
			w.Header().Set(api.TraceIDHeader, spanContext.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}
