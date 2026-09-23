package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreLogEvents_RoundTripIdempotencyAndCursor(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	accountID := uuid.NewString()
	appID := uuid.NewString()
	deploymentID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Microsecond)
	latency := 37

	httpEvent, err := store.InsertLogEvent(ctx, state.LogEvent{
		OccurredAt: now.Add(-time.Minute), AccountID: accountID, AppID: appID,
		DeploymentID: deploymentID, Source: state.LogEventSourceHTTP,
		SourceEventID: "req:1", RequestID: "req_1", TraceID: "trace_1",
		Route: "/checkout", Method: "POST", Status: 503,
		Message: "POST /checkout returned 503", LatencyMS: &latency,
		Fields: json.RawMessage(`{"region":"eu"}`),
	})
	if err != nil {
		t.Fatalf("InsertLogEvent HTTP: %v", err)
	}
	replayed, err := store.InsertLogEvent(ctx, state.LogEvent{
		OccurredAt: now.Add(-time.Minute), AccountID: accountID, AppID: appID,
		DeploymentID: deploymentID, Source: state.LogEventSourceHTTP,
		SourceEventID: "req:1", RequestID: "req_1", Route: "/checkout",
		Status: 503, Message: "retry payload is ignored by idempotency",
	})
	if err != nil {
		t.Fatalf("InsertLogEvent replay: %v", err)
	}
	if replayed.ID != httpEvent.ID || replayed.Message != httpEvent.Message {
		t.Fatalf("replay = %+v, want original %+v", replayed, httpEvent)
	}

	runtimeEvent, err := store.InsertLogEvent(ctx, state.LogEvent{
		OccurredAt: now.Add(-2 * time.Minute), AccountID: accountID, AppID: appID,
		DeploymentID: deploymentID, InstanceID: "inst-1",
		Source: state.LogEventSourceRuntime, SourceEventID: "inst-1:7",
		Level: "error", Stream: "stderr", Message: "database unavailable",
	})
	if err != nil {
		t.Fatalf("InsertLogEvent runtime: %v", err)
	}

	filter := state.LogEventFilter{
		AccountID: accountID, AppID: appID,
		Since: now.Add(-time.Hour), Until: now, Limit: 1,
	}
	first, hasMore, err := store.ListLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents first: %v", err)
	}
	if !hasMore || len(first) != 1 || first[0].ID != httpEvent.ID {
		t.Fatalf("first = %+v, hasMore=%v", first, hasMore)
	}
	if first[0].LatencyMS == nil || *first[0].LatencyMS != latency || string(first[0].Fields) == "" {
		t.Fatalf("HTTP projection lost structured fields: %+v", first[0])
	}

	filter.BeforeAt = first[0].OccurredAt
	filter.BeforeID = first[0].ID
	second, hasMore, err := store.ListLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents second: %v", err)
	}
	if hasMore || len(second) != 1 || second[0].ID != runtimeEvent.ID {
		t.Fatalf("second = %+v, hasMore=%v", second, hasMore)
	}

	filter.BeforeAt = time.Time{}
	filter.BeforeID = ""
	filter.Limit = 20
	filter.RequestID = "trace_1"
	filter.Status = 503
	filter.Route = "/checkout"
	filtered, hasMore, err := store.ListLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents filtered: %v", err)
	}
	if hasMore || len(filtered) != 1 || filtered[0].ID != httpEvent.ID {
		t.Fatalf("filtered = %+v, hasMore=%v", filtered, hasMore)
	}
}
