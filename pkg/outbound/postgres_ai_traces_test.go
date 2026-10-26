package outbound

// adr: 20261020202035 — Postgres settles traced calls into their trace's
// row with the streak of identical tool requests, admits against per-trace
// call, cost and tool-loop limits with the trace's live leases, caps the
// new traces of a day, reads traces back through the store, checks trace
// IDs in the database, and adds no round trip to a traced call.

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresAITraces(t *testing.T) {
	calls, cost, repeats := int64(4), int64(1_000), int64(2)
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true,
		TraceMaxCalls: &calls, TraceMaxCost: &cost, TraceMaxRepeatedToolCalls: &repeats})
	ctx := context.Background()
	backend, err := NewPostgresBackend(f.pool)
	if err != nil {
		t.Fatal(err)
	}
	integration, err := f.integration(f.offer.ID)
	if err != nil || integration.traceLimits() != (aiTraceLimits{calls: 4, cost: 1_000, repeats: 2}) {
		t.Fatalf("resolved trace limits = %+v, %v", integration.traceLimits(), err)
	}
	// admit admits a call of a trace the app named with X-Gregale-AI-Trace.
	admit := func(traceID string, reserve int64) Decision {
		t.Helper()
		spec := f.spec()
		spec.AITraceID, spec.AITraceLimits, spec.AIReservedCostMillicents = traceID, integration.traceLimits(), reserve
		spec.AITraceNamed = true
		decision, err := backend.Admit(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}
	signature := bytes.Repeat([]byte{7}, 16)
	settle := func(lease, traceID string, outcome AIUsageOutcome, tools []string, signature []byte) {
		t.Helper()
		record := AIUsageRecord{IntegrationID: f.offer.ID, Caller: f.app.ID, Outcome: outcome, TraceID: traceID, ToolCalls: tools,
			ToolSignature: signature, Usage: AIUsage{Model: "gpt-4o", Known: outcome == AIUsageOutcomeKnown, InputTokens: 20, OutputTokens: 10},
			CostMillicents: 100, LeaseID: lease, Log: &AIRequestLog{Status: http.StatusOK, Attempts: 1}}
		if _, err := backend.RecordAIUsageAlerts(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	// Two identical tool requests in a row reach the tool-loop limit.
	for range 2 {
		decision := admit("task-1", 100)
		if !decision.Granted {
			t.Fatalf("task-1 admission = %+v", decision)
		}
		settle(decision.LeaseID, "task-1", AIUsageOutcomeKnown, []string{"lookup"}, signature)
	}
	if refused := admit("task-1", 100); refused.Granted || refused.Reason != ReasonTraceToolLoop {
		t.Fatalf("a tool loop = %+v", refused)
	}
	// Calls in flight count against the call and cost limits.
	for index := range 4 {
		if decision := admit("task-2", 0); !decision.Granted {
			t.Fatalf("task-2 call %d = %+v", index+1, decision)
		}
	}
	if refused := admit("task-2", 0); refused.Granted || refused.Reason != ReasonTraceCallLimit {
		t.Fatalf("past the call limit = %+v", refused)
	}
	if decision := admit("task-3", 600); !decision.Granted {
		t.Fatalf("task-3 = %+v", decision)
	}
	if refused := admit("task-3", 600); refused.Granted || refused.Reason != ReasonTraceCostLimit {
		t.Fatalf("past the cost limit = %+v", refused)
	}
	// No tools end a streak; an error leaves it.
	for _, step := range []struct {
		outcome   AIUsageOutcome
		signature []byte
	}{{AIUsageOutcomeKnown, signature}, {AIUsageOutcomeKnown, nil}, {AIUsageOutcomeKnown, signature}, {AIUsageOutcomeError, nil}} {
		decision := admit("task-4", 0)
		if !decision.Granted {
			t.Fatalf("task-4 = %+v", decision)
		}
		var tools []string
		if step.signature != nil {
			tools = []string{"lookup"}
		}
		settle(decision.LeaseID, "task-4", step.outcome, tools, step.signature)
	}

	traces, err := f.store.ListOutboundAITraces(ctx, f.account.ID, f.offer.ID, state.OutboundAITraceQuery{Limit: 10})
	if err != nil || len(traces) != 2 || traces[0].TraceID != "task-4" || traces[1].TraceID != "task-1" {
		t.Fatalf("traces = %+v, %v", traces, err)
	}
	if four := traces[0]; four.Calls != 4 || four.ErrorCalls != 1 || four.RepeatedToolCalls != 1 || four.ToolCalls != 2 {
		t.Fatalf("task-4 = %+v", four)
	}
	one := traces[1]
	if one.Calls != 2 || one.CostMillicents != 200 || one.InputTokens != 40 || one.RepeatedToolCalls != 2 || one.ToolCalls != 2 ||
		one.Caller != f.app.ID || strings.Join(one.Models, ",") != "gpt-4o" || one.LastAt.Before(one.FirstAt) {
		t.Fatalf("task-1 = %+v", one)
	}
	if mine, err := f.store.ListOutboundAITraces(ctx, f.account.ID, f.offer.ID, state.OutboundAITraceQuery{AppID: f.app.ID, Before: &one.LastAt}); err != nil ||
		len(mine) != 0 {
		t.Fatalf("traces before task-1 = %+v, %v", mine, err)
	}
	detail, err := f.store.GetOutboundAITrace(ctx, f.account.ID, f.offer.ID, "task-1")
	if err != nil || detail.Trace.Calls != 2 || len(detail.Calls) != 2 || detail.Calls[0].TraceID != "task-1" ||
		strings.Join(detail.Calls[0].ToolCalls, ",") != "lookup" || detail.Calls[0].CreatedAt.After(detail.Calls[1].CreatedAt) {
		t.Fatalf("task-1 detail = %+v, %v", detail, err)
	}
	if _, err := f.store.GetOutboundAITrace(ctx, "00000000-0000-0000-0000-000000000000", f.offer.ID, "task-1"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's trace = %v", err)
	}

	// Past the day's new traces, a new trace is refused under limits and
	// not recorded; known traces still settle.
	if _, err := f.pool.Exec(ctx, `UPDATE outbound_ai_trace_days SET traces = $2 WHERE integration_id = $1`,
		f.offer.ID, api.OutboundAITracesPerDay); err != nil {
		t.Fatal(err)
	}
	if refused := admit("task-new", 0); refused.Granted || refused.Reason != ReasonTraceUntracked {
		t.Fatalf("an untracked trace = %+v", refused)
	}
	// A traceparent trace can come from the app's own clients: it goes on
	// untracked instead (ADR 20261020202035, amended).
	traceparent := f.spec()
	traceparent.AITraceID, traceparent.AITraceLimits = "4bf92f3577b34da6a3ce929d0e0e4736", integration.traceLimits()
	if granted, err := backend.Admit(ctx, traceparent); err != nil || !granted.Granted {
		t.Fatalf("an untracked traceparent trace = %+v, %v", granted, err)
	} else if err := backend.Release(ctx, f.offer.ID, granted.LeaseID); err != nil {
		t.Fatal(err)
	}
	settle("", "task-new", AIUsageOutcomeKnown, nil, nil)
	settle("", "task-4", AIUsageOutcomeKnown, nil, nil)
	if _, err := f.store.GetOutboundAITrace(ctx, f.account.ID, f.offer.ID, "task-new"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a trace past the day's cap = %v", err)
	}
	if detail, err := f.store.GetOutboundAITrace(ctx, f.account.ID, f.offer.ID, "task-4"); err != nil || detail.Trace.Calls != 5 {
		t.Fatalf("a known trace past the day's cap = %+v, %v", detail.Trace, err)
	}

	// The database checks trace IDs and tool signatures.
	for _, statement := range []string{
		`INSERT INTO outbound_ai_traces (integration_id, trace_id, caller) VALUES ($1, 'has space', 'app')`,
		`INSERT INTO outbound_ai_traces (integration_id, trace_id, caller, tool_signature) VALUES ($1, 'ok', 'app', '\x01')`,
		`UPDATE outbound_admission_leases SET ai_trace_id = 'has space' WHERE integration_id = $1`,
	} {
		if _, err := f.pool.Exec(ctx, statement, f.offer.ID); err == nil {
			t.Errorf("%s was accepted", statement)
		}
	}
}

func TestPostgresTracedCallsAddNoRoundTrip(t *testing.T) {
	calls := int64(1_000)
	f := newAIPreviewFixture(t, okChatCompletion, state.OutboundAIPolicy{RequestLogEnabled: true, TraceMaxCalls: &calls})
	ctx := context.Background()
	pool, tracer := f.tracedPool(2)
	handler := f.handlerOn(pool, nil, nil)
	call := func(headers ...string) *httptest.ResponseRecorder {
		principal := withRelayPrincipal(ctx, relayPrincipal{app: WorkloadIdentity{AccountID: f.account.ID, AppID: f.app.ID}})
		request := httptest.NewRequestWithContext(principal, http.MethodPost, "http://outboundd"+Prefix+f.offer.ID+"/v1/chat/completions",
			strings.NewReader(`{"model":"gpt-4o","max_tokens":10,"messages":[{"role":"user","content":"hi"}]}`))
		request.Header.Set("Content-Type", "application/json")
		for i := 0; i+1 < len(headers); i += 2 {
			request.Header.Set(headers[i], headers[i+1])
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	// The first call adopts the policy revision and prepares statements.
	if got := call(api.OutboundAITraceHeader, "warm"); got.Code != http.StatusOK {
		t.Fatalf("first call = %d %s", got.Code, got.Body.String())
	}
	const rounds = 5
	measure := func(headers ...string) int64 {
		tracer.roundTrips.Store(0)
		for i := range rounds {
			if got := call(headers...); got.Code != http.StatusOK {
				t.Fatalf("call %d = %d %s", i, got.Code, got.Body.String())
			}
		}
		return tracer.roundTrips.Load()
	}
	untraced, traced := measure(), measure(api.OutboundAITraceHeader, "task-1")
	t.Logf("%d round trips untraced, %d traced over %d calls", untraced, traced, rounds)
	if traced > untraced {
		t.Fatalf("traced calls took %d round trips, untraced %d", traced, untraced)
	}
	if detail, err := f.store.GetOutboundAITrace(ctx, f.account.ID, f.offer.ID, "task-1"); err != nil || detail.Trace.Calls != rounds {
		t.Fatalf("task-1 = %+v, %v", detail.Trace, err)
	}
}
