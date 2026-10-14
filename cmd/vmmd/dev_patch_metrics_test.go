package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// adr: 740 — only a generation's first acknowledgement is measured, and the
// guest-reported error code cannot widen the result label.
func TestDevPatchMetricsCountFirstAcknowledgementOnly(t *testing.T) {
	f := newRuntimeDevPatchFixture(t, true, true)
	reg := prometheus.NewRegistry()
	f.receiver.devPatchMetrics = newDevPatchMetrics(reg)
	f.publish(t, "first", false)
	for range 2 {
		response := sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchAckKind, PatchGeneration: 1, PatchApplyMS: 40})
		if !response.Accepted {
			t.Fatalf("ack response = %+v", response)
		}
	}
	if got := testutil.ToFloat64(f.receiver.devPatchMetrics.acks.WithLabelValues("applied")); got != 1 {
		t.Fatalf("applied acks = %v, want 1 for two acknowledgements of one generation", got)
	}
	if got := testutil.CollectAndCount(f.receiver.devPatchMetrics.delivery); got != 1 {
		t.Fatalf("delivery series = %d, want 1", got)
	}
	f.publish(t, "second", false)
	sendRuntimeConfigTestRequest(t, f.receiver, runtimeConfigRequest{Kind: runtimeDevPatchAckKind, PatchGeneration: 2, ErrorCode: "tenant_chosen_code"})
	if got := testutil.ToFloat64(f.receiver.devPatchMetrics.acks.WithLabelValues("other")); got != 1 {
		t.Fatalf("other acks = %v, want 1", got)
	}
}

func TestDevPatchAckResultIsBounded(t *testing.T) {
	for code, want := range map[string]string{"": "applied", "apply_failed": "apply_failed", "restart_failed": "restart_failed", "invalid_patch": "invalid_patch", "x": "other", "other": "other", "applied": "other"} {
		if got := devPatchAckResult(code); got != want {
			t.Fatalf("devPatchAckResult(%q) = %q, want %q", code, got, want)
		}
	}
	var nilMetrics *devPatchMetrics
	nilMetrics.observeAck("", 0, 0) // nil-safe
}
