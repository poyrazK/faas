// Tests for pkg/wire/slog_bridge.go (issue #555 PR-3).
package wire_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/wire"
)

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
	bridge := wire.NewSlogBridge(base.Handler())
	log := slog.New(bridge)

	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "bridge.test")
	// slog.Logger.Info reads its context from the record's
	// per-call ctx; the bridge reads from the record's per-call ctx
	// (slog.Logger.LogAttrs(ctx, ...) — the slog idiom for
	// contextual emit).
	ctx := oteltrace.ContextWithSpanContext(context.Background(), span.SpanContext())
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
	bridge := wire.NewSlogBridge(base.Handler())
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
