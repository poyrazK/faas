package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

func testLogEventInsertAndList(t *testing.T, fx *Fixture) {
	t.Helper()

	now := time.Now().UTC().Truncate(time.Microsecond)
	otherAccountID := uuid.NewString()
	events := []state.LogEvent{
		{
			OccurredAt: now.Add(-time.Minute), AccountID: fx.Account.ID, AppID: fx.App.ID,
			DeploymentID: fx.Deployment.ID, Source: state.LogEventSourceHTTP, SourceEventID: "http:checkout:1",
			RequestID: "req_checkout_1", Route: "/checkout", Method: "POST", Status: 500,
			Message: "checkout returned 500",
		},
		{
			OccurredAt: now.Add(-2 * time.Minute), AccountID: fx.Account.ID, AppID: fx.App.ID,
			Source: state.LogEventSourceRuntime, SourceEventID: "runtime:ready:1", Level: "info",
			Message: "instance ready",
		},
		{
			OccurredAt: now.Add(-3 * time.Minute), AccountID: fx.Account.ID, AppID: fx.App.ID,
			Source: state.LogEventSourceDNS, SourceEventID: "dns:propagated:1", Message: "record propagated",
		},
		{
			OccurredAt: now.Add(-30 * time.Second), AccountID: otherAccountID, AppID: fx.App.ID,
			Source: state.LogEventSourceHTTP, SourceEventID: "http:other-tenant:1", Status: 500,
			Message: "must remain account scoped",
		},
	}
	inserted := make([]state.LogEvent, 0, len(events))
	for _, event := range events {
		got, err := fx.Store.InsertLogEvent(fx.Ctx, event)
		if err != nil {
			t.Fatalf("InsertLogEvent: %v", err)
		}
		inserted = append(inserted, got)
	}

	replay := events[0]
	replay.ID = uuid.NewString()
	replay.Message = "replay must not replace the first event"
	replayed, err := fx.Store.InsertLogEvent(fx.Ctx, replay)
	if err != nil {
		t.Fatalf("InsertLogEvent replay: %v", err)
	}
	if replayed.ID != inserted[0].ID || replayed.Message != events[0].Message {
		t.Fatalf("idempotent replay = %+v, want original event %+v", replayed, inserted[0])
	}

	filter := state.LogEventFilter{
		AccountID: fx.Account.ID, AppID: fx.App.ID,
		Since: now.Add(-time.Hour), Until: now.Add(time.Second), Limit: 2,
	}
	page, hasMore, err := fx.Store.ListLogEvents(fx.Ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents first page: %v", err)
	}
	if !hasMore || len(page) != 2 || page[0].ID != inserted[0].ID || page[1].ID != inserted[1].ID {
		t.Fatalf("first page = %+v hasMore=%v, want newest two tenant events and more=true", page, hasMore)
	}

	filter.BeforeAt = page[1].OccurredAt
	filter.BeforeID = page[1].ID
	secondPage, hasMore, err := fx.Store.ListLogEvents(fx.Ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents second page: %v", err)
	}
	if hasMore || len(secondPage) != 1 || secondPage[0].ID != inserted[2].ID {
		t.Fatalf("second page = %+v hasMore=%v, want final tenant event and more=false", secondPage, hasMore)
	}

	filter.BeforeAt = time.Time{}
	filter.BeforeID = ""
	filter.Limit = 10
	filter.Source = state.LogEventSourceHTTP
	filter.Route = "/checkout"
	filter.Status = 500
	filtered, hasMore, err := fx.Store.ListLogEvents(fx.Ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents filtered: %v", err)
	}
	if hasMore || len(filtered) != 1 || filtered[0].ID != inserted[0].ID {
		t.Fatalf("filtered events = %+v hasMore=%v, want only matching checkout request", filtered, hasMore)
	}
}
