package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

const (
	logTestAccountID      = "11111111-1111-4111-8111-111111111111"
	logTestOtherAccountID = "55555555-5555-4555-8555-555555555555"
	logTestAppID          = "22222222-2222-4222-8222-222222222222"
	logTestOtherAppID     = "33333333-3333-4333-8333-333333333333"
	logTestDeploymentID   = "44444444-4444-4444-8444-444444444444"
)

func TestMemStoreLogEvents_IdempotentTenantQueryAndCursor(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	base := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	latency := 41
	events := []LogEvent{
		{
			OccurredAt: base.Add(-time.Minute), AccountID: logTestAccountID, AppID: logTestAppID,
			DeploymentID: logTestDeploymentID, Source: LogEventSourceHTTP, SourceEventID: "req:one",
			RequestID: "req_1", Route: "/checkout", Method: "POST", Status: 500,
			Message: "POST /checkout returned 500", LatencyMS: &latency, Fields: json.RawMessage(`{"region":"eu"}`),
		},
		{
			OccurredAt: base.Add(-2 * time.Minute), AccountID: logTestAccountID, AppID: logTestAppID,
			DeploymentID: logTestDeploymentID, Source: LogEventSourceRuntime, SourceEventID: "inst:7",
			InstanceID: "inst-1", Level: "ERROR", Stream: "STDERR", Message: "database unavailable",
		},
		{
			OccurredAt: base.Add(-3 * time.Minute), AccountID: logTestAccountID, AppID: logTestAppID,
			Source: LogEventSourceDNS, SourceEventID: "dns:1", Message: "record propagated",
		},
		{
			OccurredAt: base.Add(-30 * time.Second), AccountID: logTestAccountID, AppID: logTestOtherAppID,
			Source: LogEventSourceHTTP, SourceEventID: "other:1", Status: 500, Message: "must remain tenant scoped",
		},
		{
			OccurredAt: base.Add(-20 * time.Second), AccountID: logTestOtherAccountID, AppID: logTestAppID,
			Source: LogEventSourceHTTP, SourceEventID: "other-account:1", Status: 500, Message: "must remain account scoped",
		},
	}
	inserted := make([]LogEvent, 0, len(events))
	for _, event := range events {
		got, err := store.InsertLogEvent(ctx, event)
		if err != nil {
			t.Fatalf("InsertLogEvent: %v", err)
		}
		inserted = append(inserted, got)
	}
	if inserted[1].Level != "error" || inserted[1].Stream != "stderr" || inserted[0].Occurrences != 1 {
		t.Fatalf("normalized events = %+v", inserted)
	}

	replayed, err := store.InsertLogEvent(ctx, events[0])
	if err != nil {
		t.Fatalf("idempotent InsertLogEvent: %v", err)
	}
	if replayed.ID != inserted[0].ID {
		t.Fatalf("replay id = %q, want %q", replayed.ID, inserted[0].ID)
	}

	filter := LogEventFilter{
		AccountID: logTestAccountID, AppID: logTestAppID,
		Since: base.Add(-time.Hour), Until: base, Limit: 2,
	}
	page, hasMore, err := store.ListLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents: %v", err)
	}
	if !hasMore || len(page) != 2 {
		t.Fatalf("first page len=%d hasMore=%v, want 2,true", len(page), hasMore)
	}
	if page[0].ID != inserted[0].ID || page[1].ID != inserted[1].ID {
		t.Fatalf("first page ids = %q, %q", page[0].ID, page[1].ID)
	}

	filter.BeforeAt = page[1].OccurredAt
	filter.BeforeID = page[1].ID
	second, hasMore, err := store.ListLogEvents(ctx, filter)
	if err != nil {
		t.Fatalf("ListLogEvents second page: %v", err)
	}
	if hasMore || len(second) != 1 || second[0].ID != inserted[2].ID {
		t.Fatalf("second page = %+v hasMore=%v", second, hasMore)
	}
}

func TestMemStoreLogEvents_Filters(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	_, err := store.InsertLogEvent(ctx, LogEvent{
		OccurredAt: now.Add(-time.Minute), AccountID: logTestAccountID, AppID: logTestAppID,
		DeploymentID: logTestDeploymentID, Source: LogEventSourceHTTP,
		SourceEventID: "http:1", RequestID: "req_1", TraceID: "trace_1",
		Route: "/checkout", Status: 503, Message: "unavailable",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.InsertLogEvent(ctx, LogEvent{
		OccurredAt: now.Add(-2 * time.Minute), AccountID: logTestAccountID, AppID: logTestAppID,
		Source: LogEventSourceRuntime, SourceEventID: "runtime:1", Message: "ready",
	})
	if err != nil {
		t.Fatal(err)
	}

	base := LogEventFilter{
		AccountID: logTestAccountID, AppID: logTestAppID,
		Since: now.Add(-time.Hour), Until: now, Limit: 20,
	}
	filters := []LogEventFilter{
		withLogSource(base, LogEventSourceHTTP),
		withLogDeployment(base, logTestDeploymentID),
		withLogRequest(base, "req_1"),
		withLogRequest(base, "trace_1"),
		withLogRoute(base, "/checkout"),
		withLogStatus(base, 503),
	}
	for _, filter := range filters {
		rows, more, listErr := store.ListLogEvents(ctx, filter)
		if listErr != nil {
			t.Fatalf("ListLogEvents(%+v): %v", filter, listErr)
		}
		if more || len(rows) != 1 || rows[0].Source != LogEventSourceHTTP {
			t.Fatalf("ListLogEvents(%+v) = %+v, more=%v", filter, rows, more)
		}
	}
}

func TestLogEventValidation(t *testing.T) {
	now := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	valid := LogEvent{
		AccountID: logTestAccountID, AppID: logTestAppID,
		Source: LogEventSourceRuntime, Message: "ready", OccurredAt: now,
	}
	negativeLatency := -1
	tests := []struct {
		name string
		edit func(*LogEvent)
	}{
		{name: "account", edit: func(e *LogEvent) { e.AccountID = "bad" }},
		{name: "app", edit: func(e *LogEvent) { e.AppID = "bad" }},
		{name: "deployment", edit: func(e *LogEvent) { e.DeploymentID = "bad" }},
		{name: "source", edit: func(e *LogEvent) { e.Source = "unknown" }},
		{name: "status", edit: func(e *LogEvent) { e.Status = 99 }},
		{name: "level", edit: func(e *LogEvent) { e.Level = "panic" }},
		{name: "stream", edit: func(e *LogEvent) { e.Stream = "fd3" }},
		{name: "message empty", edit: func(e *LogEvent) { e.Message = "" }},
		{name: "message large", edit: func(e *LogEvent) { e.Message = strings.Repeat("x", maxLogEventMessageRunes+1) }},
		{name: "latency", edit: func(e *LogEvent) { e.LatencyMS = &negativeLatency }},
		{name: "fields array", edit: func(e *LogEvent) { e.Fields = json.RawMessage(`[]`) }},
		{name: "fields large", edit: func(e *LogEvent) {
			e.Fields = json.RawMessage(`{"x":"` + strings.Repeat("x", maxLogEventFieldsBytes) + `"}`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := valid
			tt.edit(&event)
			if _, err := normalizeLogEventForInsert(event, now); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func withLogSource(filter LogEventFilter, source LogEventSource) LogEventFilter {
	filter.Source = source
	return filter
}

func withLogDeployment(filter LogEventFilter, deploymentID string) LogEventFilter {
	filter.DeploymentID = deploymentID
	return filter
}

func withLogRequest(filter LogEventFilter, requestID string) LogEventFilter {
	filter.RequestID = requestID
	return filter
}

func withLogRoute(filter LogEventFilter, route string) LogEventFilter {
	filter.Route = route
	return filter
}

func withLogStatus(filter LogEventFilter, status int) LogEventFilter {
	filter.Status = status
	return filter
}
