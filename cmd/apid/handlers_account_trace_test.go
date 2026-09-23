package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountTraceLookupIncludesDurableInvocationRows(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "trace-app")
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: appID, AccountID: e.acct.ID, Source: state.InvocationQueue,
		QueueName: "orders", Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"4bf92f3577b34da6a3ce929d0e0e4736","traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`),
		DueAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	asyncInv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
		Headers: json.RawMessage(`{"X-Gregale-Trace-Id":"4bf92f3577b34da6a3ce929d0e0e4736","traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"}`),
		DueAt:   time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation async: %v", err)
	}

	rec := e.do(t, http.MethodGet, "/v1/account/traces/"+traceID, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountTraceLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range out.Invocations {
		seen[item.ID] = true
	}
	if len(out.Invocations) != 2 || !seen[inv.ID] || !seen[asyncInv.ID] {
		t.Fatalf("invocations = %+v, want %s and %s", out.Invocations, inv.ID, asyncInv.ID)
	}
	if out.Invocations[0].Traceparent == "" || out.Invocations[1].Traceparent == "" || !out.Partial {
		t.Fatalf("invocation projection = %+v, partial=%v; MemStore telemetry should be enrichment-only", out.Invocations, out.Partial)
	}
}

func TestAccountTraceLookupIncludesSafeHTTPLogEvents(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "trace-logs-app")
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	now := time.Now().UTC()
	for _, event := range []state.LogEvent{
		{
			OccurredAt: now.Add(-time.Second), AccountID: e.acct.ID, AppID: appID,
			Source: state.LogEventSourceHTTP, SourceEventID: "http:matching", RequestID: traceID, TraceID: traceID,
			Route: "GET /checkout", Method: "GET", Status: 503, Message: "GET /checkout returned 503",
		},
		{
			OccurredAt: now.Add(-500 * time.Millisecond), AccountID: e.acct.ID, AppID: appID,
			Source: state.LogEventSourceHTTP, SourceEventID: "http:matching-newer", RequestID: traceID, TraceID: traceID,
			Route: "GET /checkout", Method: "GET", Status: 200, Message: "GET /checkout returned 200",
		},
		{
			OccurredAt: now.Add(-2 * time.Second), AccountID: e.acct.ID, AppID: appID,
			Source: state.LogEventSourceHTTP, SourceEventID: "http:request-id-not-trace", RequestID: traceID, TraceID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			Route: "GET /other", Method: "GET", Status: 200, Message: "unrelated trace",
		},
		{
			OccurredAt: now.Add(-3 * time.Second), AccountID: e.acct.ID, AppID: appID,
			Source: state.LogEventSourceRuntime, SourceEventID: "runtime:matching", TraceID: traceID,
			Stream: "stderr", Message: "runtime logs are outside the HTTP-only trace projection",
		},
		{
			OccurredAt: now.Add(-4 * time.Second), AccountID: "55555555-5555-4555-8555-555555555555", AppID: appID,
			Source: state.LogEventSourceHTTP, SourceEventID: "http:other-account", RequestID: traceID, TraceID: traceID,
			Route: "GET /private", Method: "GET", Status: 200, Message: "other tenant event",
		},
	} {
		if _, err := e.store.InsertLogEvent(context.Background(), event); err != nil {
			t.Fatalf("InsertLogEvent: %v", err)
		}
	}

	rec := e.do(t, http.MethodGet, "/v1/account/traces/"+traceID+"?limit=1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountTraceLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Logs) != 1 || !out.LogsTruncated {
		t.Fatalf("logs = %+v truncated=%v, want newest matching HTTP event and truncation marker", out.Logs, out.LogsTruncated)
	}
	got := out.Logs[0]
	if got.App != "trace-logs-app" || got.TraceID != traceID || got.Source != api.LogSourceHTTP || got.Status != 200 || got.Method != "GET" || got.ID == "" {
		t.Fatalf("trace log = %+v", got)
	}
}
