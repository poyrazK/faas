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
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/metadata"

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
	registry := prometheus.NewRegistry()
	h, err := otelinit.Init(context.Background(), otelinit.Config{
		Name:              "test-daemon",
		Version:           "1.0.0",
		MetricsRegisterer: registry,
		MetricPrefix:      "test_daemon",
	}, captureLogs(&buf))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}

	// The deployment counter is independent of the exporter
	// configuration (issue #555 acceptance #5) — it must be
	// non-nil on the no-op path so the watcher can wire against it.
	if h.DeploymentCounter == nil {
		t.Error("Handle.DeploymentCounter is nil on no-op path; want non-nil")
	}

	// Shutdown must not error and must not block.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}

	// The boot log must announce the no-op path so the operator
	// can see "no exporter configured" in the daemon log.
	logs := buf.String()
	if !strings.Contains(logs, "no-op") {
		t.Errorf("expected no-op boot line in logs, got: %s", logs)
	}
	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather trace health metrics: %v", err)
	}
	for _, family := range families {
		if family.GetName() != "test_daemon_otel_trace_exporter_enabled" && family.GetName() != "test_daemon_otel_trace_exporter_up" {
			continue
		}
		if len(family.GetMetric()) != 1 || family.GetMetric()[0].GetGauge().GetValue() != 0 {
			t.Errorf("%s should be registered at 0 on noop path: %+v", family.GetName(), family)
		}
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
	h, err := otelinit.Init(context.Background(), otelinit.Config{Name: "test-daemon", Version: "1.0.0"}, captureLogs(&buf))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	if h.DeploymentCounter == nil {
		t.Error("Handle.DeploymentCounter is nil on OTLP path; want non-nil")
	}

	// Emit one span and force a flush.
	tracer := otelinit.Tracer("test-daemon")
	_, span := tracer.Start(context.Background(), "test.span")
	span.End()

	// Shutdown flushes the batch.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.Shutdown(ctx); err != nil {
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

// TestLiftFromMD pins the gRPC-MD round trip: a context carrying
// inbound x-faas-* metadata lifts to the expected trace_id/span_id
// pair (issue #555 review: the previous revision built a carrier
// LiftFromMD never read, asserting only the empty-md path).
func TestLiftFromMD(t *testing.T) {
	prev := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(prev) })

	const wantTID = "00000000000000000000000000000001"
	const wantSID = "0000000000000001"
	md := metadata.New(map[string]string{
		"x-faas-trace-id": wantTID,
		"x-faas-span-id":  wantSID,
	})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	ids, ok := otelinit.LiftFromMD(ctx)
	if !ok {
		t.Fatal("LiftFromMD on a metadata-bearing ctx returned ok=false")
	}
	if ids.TraceID != wantTID {
		t.Errorf("TraceID = %q, want %q", ids.TraceID, wantTID)
	}
	if ids.SpanID != wantSID {
		t.Errorf("SpanID = %q, want %q", ids.SpanID, wantSID)
	}
}

// TestLiftFromMD_EmptyMD pins the negative contract: a context
// without inbound metadata returns ok=false and zero-valued IDs.
// A regression here would mean the slog envelope starts showing
// bogus trace_ids on every request.
func TestLiftFromMD_EmptyMD(t *testing.T) {
	ids, ok := otelinit.LiftFromMD(context.Background())
	if ok {
		t.Errorf("expected ok=false on bg ctx, got %+v", ids)
	}
	if ids.TraceID != "" || ids.SpanID != "" {
		t.Errorf("expected zero-valued IDs, got %+v", ids)
	}
}
