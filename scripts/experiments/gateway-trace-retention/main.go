package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/onebox-faas/faas/pkg/gateway"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	if err := run(); err != nil {
		slog.Error("trace retention reproduction failed", "err", err)
		os.Exit(1)
	}
}

func run() (err error) {
	ring := gateway.NewTraceRing(gateway.DefaultTraceRingCap)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(gateway.NewTraceRingExporter(ring, logger)), sdktrace.WithSampler(sdktrace.AlwaysSample()))
	defer func() { err = errors.Join(err, tp.Shutdown(context.Background())) }()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	child := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }), "gateway.route")
	outer := otelhttp.NewHandler(child, "gatewayd-public.handler")
	var baseline uint64
	start := time.Now()
	for i := 0; i <= 100000; i++ {
		if i > 0 {
			req := httptest.NewRequest("GET", fmt.Sprintf("http://e2e-go.gregale.dev/?load=extended&sample=%d", i), nil)
			req.Header.Set("Traceparent", fmt.Sprintf("00-%032x-%016x-01", i, i))
			req.Header.Set("User-Agent", "k6/1.5.0")
			outer.ServeHTTP(httptest.NewRecorder(), req)
		}
		if i%10000 == 0 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if i == 0 {
				baseline = m.HeapAlloc
			}
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"requests": i, "traces": ring.Len(), "heap_live_bytes": m.HeapAlloc, "heap_growth_bytes": m.HeapAlloc - baseline, "go_total_bytes": m.Sys - m.HeapReleased, "elapsed_ms": time.Since(start).Milliseconds()}); err != nil {
				return fmt.Errorf("encode memory sample: %w", err)
			}
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "--heap-profile" {
		f, err := os.Create("trace-retention.pprof")
		if err != nil {
			return fmt.Errorf("create heap profile: %w", err)
		}
		if err := errors.Join(pprof.WriteHeapProfile(f), f.Close()); err != nil {
			return fmt.Errorf("write heap profile: %w", err)
		}
	}
	runtime.KeepAlive(ring)
	return nil
}
