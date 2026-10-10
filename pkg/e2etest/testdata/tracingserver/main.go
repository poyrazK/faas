// tracingserver is the ADR-957 native acceptance fixture: a scratch-image
// HTTP app with an OpenTelemetry SDK compiled in and no tracing
// configuration of its own. It relies entirely on the OTEL_* environment
// guest-init stamps, so a span reaching the debugger proves the guest bridge,
// vmmd broker and apid ingest path end to end.
//
// Every request continues the inbound W3C traceparent, records a client span
// named "SELECT orders" (a stand-in for a database call) and flushes before
// responding, so the test does not wait on the batch processor.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if !strings.HasPrefix(port, ":") {
		port = ":" + port
	}
	// The exporter reads OTEL_EXPORTER_OTLP_TRACES_ENDPOINT; the sampler
	// honors OTEL_TRACES_SAMPLER, both stamped by guest-init.
	exporter, err := otlptracehttp.New(context.Background())
	if err != nil {
		log.Fatalf("tracingserver: exporter: %v", err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	otel.SetTracerProvider(provider)
	propagator := propagation.TraceContext{}
	tracer := provider.Tracer("gregale-e2e-tracingserver")

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		ctx := propagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, server := tracer.Start(ctx, r.Method+" "+r.URL.Path, trace.WithSpanKind(trace.SpanKindServer))
		_, query := tracer.Start(ctx, "SELECT orders", trace.WithSpanKind(trace.SpanKindClient),
			trace.WithAttributes(attribute.String("db.system", "postgresql"), attribute.String("db.statement", "SELECT * FROM orders WHERE id = $1")))
		time.Sleep(40 * time.Millisecond)
		query.End()
		server.End()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("traced " + server.SpanContext().TraceID().String() + "\n"))
	})
	log.Printf("tracingserver: listening on %s", port)
	log.Fatal(http.ListenAndServe(port, mux))
}
