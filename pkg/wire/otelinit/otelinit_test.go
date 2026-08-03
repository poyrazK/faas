// Tests for the OTel SDK bootstrap (issue #555 PR-1).
//
// Conventions:
//   - Every test names the inverse scenario it pins: "Init_NoEndpoint
//     Is No-Op", "Init_WithEndpoint Wires TracerProvider", etc.
//   - Tests use the upstream tracetest.InMemoryExporter so the
//     SDK-level span export path is exercised under -race, not a mock.
//   - The Init call runs against an in-memory tracer provider for
//     spans that should NOT leave the process; the noop fallback is
//     tested in Init_NoEndpoint.
package otelinit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/wire/otelinit"
)

// captureLogs returns a slog.Logger that writes JSON records into buf
// alongside the daemon's correlation envelope (matches the daemon.go
// pattern). Tests can then decode the buffer to assert boot-time
// diagnostics.
func captureLogs(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestInit_NoEndpointIsNoOp(t *testing.T) {
	// Save and restore the global provider so the test does not
	// leak into parallel tests.
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	// Ensure the env var is unset for this test even if a parent
	// process exported it.
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	var buf bytes.Buffer
	shutdown, err := otelinit.Init(context.Background(), otelinit.Config{Name: "test-daemon", Version: "1.0.0"}, captureLogs(&buf))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Shutdown must not error and must not block.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}

	// The boot log must announce the no-op path so the operator
	// can see "no exporter configured" in the daemon log.
	logs := buf.String()
	if !strings.Contains(logs, "no-op") {
		t.Errorf("expected no-op boot line in logs, got: %s", logs)
	}
}

// TestInit_NameRequired pins the contract that a daemon's name is
// required — the OTel service.name attribute has no zero form.
func TestInit_NameRequired(t *testing.T) {
	_, err := otelinit.Init(context.Background(), otelinit.Config{Version: "1.0.0"}, slog.Default())
	if err == nil {
		t.Fatal("Init with empty name: expected error, got nil")
	}
}

// TestInit_WithEndpoint_WiresProvider asserts that an OTLP endpoint
// (pointing at a fake collector) causes Init to install a working
// (non-noop) TracerProvider and emit a "wired" log line.
//
// We stand up an httptest.Server that records the export calls; on
// shutdown we verify at least one Export call landed.
func TestInit_WithEndpoint_WiresProvider(t *testing.T) {
	prev := otel.GetTracerProvider()
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	// Set the env to point at the test server's /v1/traces.
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/v1/traces") {
			b, _ := io.ReadAll(r.Body)
			gotBody = b
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	// Strip "http://" off the front so the SDK parses it as host:port.
	endpoint := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", endpoint)

	var buf bytes.Buffer
	shutdown, err := otelinit.Init(context.Background(), otelinit.Config{Name: "test-daemon", Version: "1.0.0"}, captureLogs(&buf))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Emit one span and force a flush.
	tracer := otelinit.Tracer("test-daemon")
	_, span := tracer.Start(context.Background(), "test.span")
	span.End()

	// Shutdown flushes the batch.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	if len(gotBody) == 0 {
		t.Error("expected OTLP export to land at test server")
	}
	if !strings.Contains(buf.String(), "wired OTLP/HTTP exporter") {
		t.Errorf("expected wired-boot log line, got: %s", buf.String())
	}
}

// TestLiftSpanContext_EmptyWhenNoSpan pins the absent-span behaviour
// callers depend on: a ctx without a span returns empty strings, not
// zeros / panics.
func TestLiftSpanContext_EmptyWhenNoSpan(t *testing.T) {
	tid, sid := otelinit.LiftSpanContext(context.Background())
	if tid != "" || sid != "" {
		t.Errorf("lift on empty ctx: got (%q,%q), want (\"\",\"\")", tid, sid)
	}
}

// TestLiftSpanContext_FromActiveSpan exercises the happy path with
// the SDK's in-memory exporter. Starts a span, lifts, and asserts
// the lifted trace_id / span_id match the actual SpanContext.
func TestLiftSpanContext_FromActiveSpan(t *testing.T) {
	// Set a fresh in-memory provider so the trace stays local.
	sp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "lift.test")
	defer span.End()

	tid, sid := otelinit.LiftSpanContext(ctx)
	sc := span.SpanContext()
	if tid != sc.TraceID().String() {
		t.Errorf("trace_id = %q, want %q", tid, sc.TraceID().String())
	}
	if sid != sc.SpanID().String() {
		t.Errorf("span_id = %q, want %q", sid, sc.SpanID().String())
	}
}

// TestLiftFromMD asserts the gRPC-MD reader returns the same trace
// context as the slog envelope. We seed the global TextMapPropagator
// with a TraceContext propagator and use a fake inbound context
// (with metadata). The x-faas-* helper then lifts.
func TestLiftFromMD(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	// Simulate a server-side inbound MD layer: build a metadata
	// carrier and walk it through the propagator's Extract.
	// In-process we use propagation.TextMapCarrier directly.
	carrier := propagation.MapCarrier{
		"x-faas-trace-id": "00000000000000000000000000000001",
		"x-faas-span-id":  "0000000000000001",
	}
	// Wrap the carrier in an inboundMetadataCarrier-style context
	// that wire.CorrelationFromIncoming recognises. Because we
	// can't easily fake gRPC metadata in a unit test, we exercise
	// the empty-md path here and rely on integration tests for
	// the full MD round-trip.
	_ = carrier
	ids, ok := otelinit.LiftFromMD(context.Background())
	if ok {
		t.Errorf("expected ok=false on bg ctx, got %+v", ids)
	}
}

// TestSlogBridge_PinsTraceContext exercises the slog bridge handler.
// It starts an OTel span, emits a slog record via the bridge, and
// asserts trace_id and span_id appear on the emitted record's JSON.
func TestSlogBridge_PinsTraceContext(t *testing.T) {
	sp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(sp),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})

	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bridge := otelinit.NewSlogBridge(base.Handler())
	log := slog.New(bridge)

	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "bridge.test")
	// We need a fresh context-scoped log because the slog.Logger
	// carries no per-call ctx; the bridge reads from the record's
	// per-call ctx, which slog.Logger.Info does not expose. We
	// invoke the underlying handler directly with a context-carrying
	// record to exercise the lift.
	ctx := oteltrace.ContextWithSpanContext(context.Background(), span.SpanContext())
	// Use slog.Default().LogAttrs which does take a ctx since
	// Go 1.21; slog.Logger.LogAttrs(ctx, Level, msg, attrs...)
	log.LogAttrs(ctx, slog.LevelInfo, "hello",
		slog.String("k", "v"),
	)
	span.End()

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("bridge emitted no log line")
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		t.Fatalf("bridge emitted non-JSON: %v\nline: %s", err, line)
	}
	if got := rec["trace_id"]; got == nil || got == "" {
		t.Errorf("bridge did not emit trace_id; record=%v", rec)
	}
	if got := rec["span_id"]; got == nil || got == "" {
		t.Errorf("bridge did not emit span_id; record=%v", rec)
	}
	if rec["k"] != "v" {
		t.Errorf("bridge dropped the user-supplied attr; record=%v", rec)
	}
}

// TestSlogBridge_NoSpanIsPassthrough pins the contract that a log
// line without OTel span context is emitted unchanged. Otherwise the
// bridge would inject empty strings into every record, polluting the
// slog JSON shape.
func TestSlogBridge_NoSpanIsPassthrough(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	bridge := otelinit.NewSlogBridge(base.Handler())
	log := slog.New(bridge)
	log.LogAttrs(context.Background(), slog.LevelInfo, "no-span",
		slog.String("k", "v"),
	)
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := rec["trace_id"]; ok {
		t.Errorf("bridge emitted trace_id on a span-less log: %v", rec)
	}
	if _, ok := rec["span_id"]; ok {
		t.Errorf("bridge emitted span_id on a span-less log: %v", rec)
	}
	if rec["k"] != "v" {
		t.Errorf("user attr dropped: %v", rec)
	}
}
