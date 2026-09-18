package vmmdgrpc

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/sched/flowcount"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// adr: 127 — flow summaries remain bounded and are emitted as safe OTel
// events without payload, URL, header, or byte-content attributes.
func TestFlowSummaryTelemetry_MapsWireAndTraceEvent(t *testing.T) {
	rows := []flowcount.FlowSummary{{
		InstanceID: "vm-1",
		Protocol:   "tcp",
		RemoteIP:   "203.0.113.10",
		RemotePort: 443,
		State:      "ESTABLISHED",
		Direction:  "outbound",
		Count:      2,
	}}
	wireRows := flowSummariesToProto(rows)
	if len(wireRows) != 1 || wireRows[0].GetRemoteIp() != "203.0.113.10" || wireRows[0].GetRemotePort() != 443 || wireRows[0].GetCount() != 2 {
		t.Fatalf("wire rows = %#v, want one endpoint summary", wireRows)
	}

	previous := otel.GetTracerProvider()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previous)
	})

	emitFlowSummarySpan(context.Background(), map[string][]flowcount.FlowSummary{"vm-1": rows})
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	if ended[0].Name() != "vmmd.flow.snapshot" {
		t.Fatalf("span name = %q, want vmmd.flow.snapshot", ended[0].Name())
	}
	if len(ended[0].Events()) != 1 || ended[0].Events()[0].Name == "" {
		t.Fatalf("span events = %#v, want one flow event", ended[0].Events())
	}
	if ended[0].Events()[0].Name != flowSummaryEventName {
		t.Fatalf("event name = %q, want %q", ended[0].Events()[0].Name, flowSummaryEventName)
	}
}
