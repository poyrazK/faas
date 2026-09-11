package main

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDebugCoverageSignal_WeightsRatesByRepresentedRequests(t *testing.T) {
	t.Parallel()
	got := debugCoverageSignal(3, 7, 10)
	if got != (api.DebugCoverageSignal{Rows: 3, Requests: 7, RatePct: 70}) {
		t.Fatalf("signal = %+v, want rows=3 requests=7 rate=70", got)
	}
	if got := debugCoverageSignal(0, 0, 0); got.RatePct != 0 {
		t.Fatalf("empty signal rate = %v, want 0", got.RatePct)
	}
}

func TestDebugCoverageTimestamp_HandlesNullableAggregate(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 9, 11, 12, 34, 56, 789000000, time.UTC)
	if got := debugCoverageTimestamp(want); got != "2026-09-11T12:34:56.789Z" {
		t.Fatalf("timestamp = %q, want RFC3339Nano value", got)
	}
	if got := debugCoverageTimestamp(nil); got != "" {
		t.Fatalf("nil timestamp = %q, want empty", got)
	}
	if got := debugCoverageTimestamp("not-a-time"); got != "" {
		t.Fatalf("unexpected timestamp type = %q, want empty", got)
	}
}
