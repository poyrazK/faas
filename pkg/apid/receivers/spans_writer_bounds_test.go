package receivers

import (
	"bytes"
	"context"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
)

// The writer only checked that a summary was JSON, and the SQL merge kept
// 1,000 spans for every plan, so one account could flood the shared database
// with oversized summaries and every plan stored the Scale cap.
func TestSpansWriterBoundsSummariesByBytesAndPlan(t *testing.T) {
	acctID := uuid.New()
	store := &fakeSpansWriterStore{}
	limiter := peraccount.NewLimiter()
	limiter.CacheLimits(acctID, api.MustLimitsFor(api.PlanHobby))
	cli := dialSpansWriterBufconn(t, store, &fakeSpansWriterMonitor{}, limiter, true)

	oversized := append(append([]byte(`["`), bytes.Repeat([]byte("x"), MaxSpansSummaryBytes)...), []byte(`"]`)...)
	_, err := cli.WriteSpansSummary(context.Background(), &apidpb.WriteSpansSummaryRequest{
		TraceId: sampleTraceID(), SummaryJson: oversized, AccountId: acctID.String(),
	})
	if status.Code(err) != codes.InvalidArgument || store.calls != 0 {
		t.Fatalf("oversized summary: err=%v store.calls=%d, want InvalidArgument before the store", err, store.calls)
	}

	resp, err := cli.WriteSpansSummary(context.Background(), &apidpb.WriteSpansSummaryRequest{
		TraceId: sampleTraceID(), SummaryJson: sampleSummary(t), AccountId: acctID.String(),
	})
	if err != nil || resp.GetOutcome() != "inserted" {
		t.Fatalf("write: %v %v", resp, err)
	}
	if want := api.MustLimitsFor(api.PlanHobby).DebugTelemetrySpansPerTrace; store.lastMax != want {
		t.Fatalf("row cap = %d, want the Hobby cap %d", store.lastMax, want)
	}
}
