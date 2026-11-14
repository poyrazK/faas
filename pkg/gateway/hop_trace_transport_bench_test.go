package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// BenchmarkHopTraceTransport measures what an otelhttp.Transport around the
// edge's hop to gatewayd-internal adds to one request. gatewayd-public wrapped
// the hop in one until 2026-11-10. The proxy already propagates the edge's
// span itself, and otelhttp v0.72 builds metric attribute sets on every
// request even with no meter provider installed. The base transport answers
// in memory, so the difference between the two cases is the wrapper alone.
func BenchmarkHopTraceTransport(b *testing.B) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	b.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, span := provider.Tracer("bench").Start(context.Background(), "public-edge")
	defer span.End()

	const body = `{"code":"app_not_found"}`
	base := hopRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusNotFound,
			ProtoMajor:    1,
			ProtoMinor:    1,
			Header:        http.Header{"Content-Type": {"application/problem+json"}},
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       r,
		}, nil
	})
	for _, bc := range []struct {
		name string
		rt   http.RoundTripper
	}{
		{"none", base},
		{"otelhttp", otelhttp.NewTransport(base, otelhttp.WithTracerProvider(provider))},
	} {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://gatewayd-internal/", nil)
				if err != nil {
					b.Fatal(err)
				}
				req.Host = "lt-edge-none.gregale.dev"
				resp, err := bc.rt.RoundTrip(req)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		})
	}
}

type hopRoundTripFunc func(*http.Request) (*http.Response, error)

func (f hopRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
