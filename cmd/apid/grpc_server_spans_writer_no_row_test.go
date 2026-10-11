package main

// adr: 957 — a missing request row is a retryable outcome, not a delivery.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSpansWriter_MissingRowIsRetryable(t *testing.T) {
	store := &fakeSpansWriterStore{updateFn: func(string, uuid.UUID, []byte) error {
		return state.ErrRequestTelemetryRowNotFound
	}}
	ops := &fakeSpansWriterMonitor{}
	cli := dialSpansWriterBufconn(t, store, ops, peraccount.NewLimiter(), true)
	resp, err := cli.WriteSpansSummary(context.Background(), &apidpb.WriteSpansSummaryRequest{
		TraceId: sampleTraceID(), SummaryJson: sampleSummary(t), AccountId: uuid.NewString(),
	})
	if err != nil {
		t.Fatalf("WriteSpansSummary: %v", err)
	}
	if resp.GetOutcome() != swOutcomeNoRow {
		t.Fatalf("outcome = %q, want %q", resp.GetOutcome(), swOutcomeNoRow)
	}
	ops.mu.Lock()
	defer ops.mu.Unlock()
	if len(ops.outcomes) != 1 || ops.outcomes[0] != swOutcomeNoRow {
		t.Fatalf("observed outcomes = %v", ops.outcomes)
	}
}
