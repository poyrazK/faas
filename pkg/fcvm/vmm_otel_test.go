// Tests for pkg/fcvm/vmm.go OTEL helpers (issue #555 PR-4).
package fcvm

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// TestTraceparentFromContext_EmptyOnNoSpan pins the contract: when no
// span is active on the boot context (legacy single-box without OTel,
// or a test fixture without an SDK install), the helper returns "" so
// the resume hook body serializes an empty traceparent that the guest
// no-ops on.
func TestTraceparentFromContext_EmptyOnNoSpan(t *testing.T) {
	if got := traceparentFromContext(context.Background()); got != "" {
		t.Errorf("traceparentFromContext(bg) = %q, want \"\"", got)
	}
}

// TestTraceparentFromContext_OnValidSpan renders the W3C wire shape
// (32-hex trace_id + 16-hex span_id + 2-hex flags). The format is
// what the runner's TRACEPARENT env expects.
func TestTraceparentFromContext_OnValidSpan(t *testing.T) {
	var tid trace.TraceID
	var sid trace.SpanID
	// Deter­min­istic trace_id + span_id so the assertion is stable.
	// All-zero is fine: SpanContext is "valid" when both IDs are non-zero.
	for i := range tid {
		tid[i] = byte(i + 1)
	}
	for i := range sid {
		sid[i] = byte(i + 1)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	ctx := trace.ContextWithSpanContext(context.Background(), sc)
	got := traceparentFromContext(ctx)
	want := "0102030405060708090a0b0c0d0e0f10-0102030405060708-01"
	if got != want {
		t.Errorf("traceparentFromContext = %q, want %q", got, want)
	}
}
