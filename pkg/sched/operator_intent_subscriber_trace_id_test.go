// pkg/sched/operator_intent_subscriber_trace_id_test.go — PR-#TBD / C3
//
// Pins the trace_id lift at the schedd subscriber's terminal outcome
// audit emit. The drainPendingOperatorIntents call path requires a
// fully wired Loop + Engine, so this test exercises the lift in
// isolation by reproducing the data-map construction with a real
// audit.Auditor backed by MemStore — the same shape the subscriber
// uses at pkg/sched/operator_intent_subscriber.go:219-244.
//
// Pins:
//
//  1. When intent.TraceID is non-nil, the subscriber's data map
//     carries `trace_id == *intent.TraceID` so the audit row's
//     jsonb payload round-trips the value end-to-end.
//  2. When intent.TraceID is nil, the data map has no `trace_id`
//     key (preserves the pre-PR contract for rows without an
//     inbound trace_id).
//  3. The pre-existing OTel-lift ordering (data["trace_id"] wins
//     over the active span context) holds when both are present
//     — explicit intent.TraceID is the authoritative value.
//
// Build tag: no Postgres required; uses MemStore.

package sched_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestOperatorIntentSubscriber_TraceIDPropagatesToOutcomeAuditRow is
// the canonical C3 pin — the schedd-side terminal audit row carries
// the OTel trace_id from the inbound intent.
func TestOperatorIntentSubscriber_TraceIDPropagatesToOutcomeAuditRow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	auditor := audit.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "test:schedd")

	const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	id, err := store.InsertOperatorIntent(
		ctx,
		state.OperatorIntentKindForcePark,
		"target-instance-id",
		nil,
		"actor-id",
		"test reason",
		json.RawMessage(`{}`),
		traceIDPtr(), // C3 — trace_id written by apid → row
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}

	// Mirror the subscriber's data-map construction at
	// pkg/sched/operator_intent_subscriber.go:219-244.
	intent, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("ClaimPendingOperatorIntent: %v", err)
	}
	if intent.ID != id {
		t.Fatalf("Claim returned id=%q, want %q", intent.ID, id)
	}
	if intent.TraceID == nil {
		t.Fatalf("Claimed.TraceID is nil; want %q", traceID)
	}
	if *intent.TraceID != traceID {
		t.Fatalf("Claimed.TraceID = %q, want %q", *intent.TraceID, traceID)
	}

	data := map[string]any{
		"actor":                 intent.ActorID,
		"intent_id":             intent.ID,
		"target_id":             intent.TargetID,
		"result":                "succeeded",
		"started_at":            intent.StartedAt,
		"finished_at":           time.Now().UTC(),
		"snap_ids_marked_stale": []string{},
	}
	if intent.TraceID != nil {
		data["trace_id"] = *intent.TraceID
	}
	auditor.Emit(ctx, "operator.action."+string(intent.Kind)+".outcome", intent.AccountID, data)

	// Verify the audit row's data jsonb carries trace_id.
	events, err := store.ListEvents(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no events emitted; want 1")
	}
	got := events[0]
	var parsed map[string]any
	if err := json.Unmarshal(got.Data, &parsed); err != nil {
		t.Fatalf("unmarshal audit data: %v", err)
	}
	if parsed["trace_id"] != traceID {
		t.Errorf("audit row data[trace_id] = %v, want %q", parsed["trace_id"], traceID)
	}
}

// TestOperatorIntentSubscriber_NilTraceIDOmittedFromOutcomeAuditRow
// pins the pre-PR contract for rows that arrive without a
// trace_id (e.g. fleet-level reclaim_build, future cron-fired
// emit sites, legacy callers). The data map must not have a
// `trace_id` key.
func TestOperatorIntentSubscriber_NilTraceIDOmittedFromOutcomeAuditRow(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	auditor := audit.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "test:schedd")

	if _, err := store.InsertOperatorIntent(
		ctx,
		state.OperatorIntentKindForcePark,
		"target-id",
		nil,
		"actor-id",
		"test",
		json.RawMessage(`{}`),
		nil, // no trace_id
	); err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	intent, err := store.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if intent.TraceID != nil {
		t.Fatalf("expected nil TraceID; got %v", *intent.TraceID)
	}
	data := map[string]any{
		"actor":                 intent.ActorID,
		"intent_id":             intent.ID,
		"target_id":             intent.TargetID,
		"result":                "succeeded",
		"started_at":            intent.StartedAt,
		"finished_at":           time.Now().UTC(),
		"snap_ids_marked_stale": []string{},
	}
	if intent.TraceID != nil {
		data["trace_id"] = *intent.TraceID
	}
	auditor.Emit(ctx, "operator.action."+string(intent.Kind)+".outcome", intent.AccountID, data)

	events, err := store.ListEvents(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no events emitted; want 1")
	}
	var parsed map[string]any
	if err := json.Unmarshal(events[0].Data, &parsed); err != nil {
		t.Fatalf("unmarshal audit data: %v", err)
	}
	if _, ok := parsed["trace_id"]; ok {
		t.Errorf("audit row data has trace_id=%v; want absent for nil-intent.TraceID rows", parsed["trace_id"])
	}
}

// TestOperatorIntentSubscriber_ExplicitTraceIDWinsOverSpanContext
// pins the precedence rule from pkg/audit/audit.go:226-228 — when
// both an active span context and an explicit data["trace_id"] are
// present, the explicit value wins. Mirrors the same rule that
// protects cron-fired sites from being clobbered by an OTel context
// the caller did not author.
func TestOperatorIntentSubscriber_ExplicitTraceIDWinsOverSpanContext(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	auditor := audit.New(store, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, "test:schedd")

	// Build a SpanContext that disagrees with our explicit trace_id.
	spanCtx := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0xaa, 0xbb, 0xcc, 0xdd, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0x00, 0xaa, 0xbb},
		SpanID:     trace.SpanID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	otelCtx := trace.ContextWithSpanContext(ctx, spanCtx)

	explicit := "4bf92f3577b34da6a3ce929d0e0e4736"
	data := map[string]any{
		"actor":     "actor-id",
		"intent_id": "intent-id",
		"target_id": "target-id",
		"result":    "succeeded",
		"trace_id":  explicit, // explicit value supplied by the lift
	}
	auditor.Emit(otelCtx, "operator.action.test", nil, data)

	events, err := store.ListEvents(ctx, "", 100)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("no events emitted; want 1")
	}
	var parsed map[string]any
	if err := json.Unmarshal(events[0].Data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := parsed["trace_id"]; got != explicit {
		t.Errorf("data[trace_id] = %v, want %q (explicit must win over span context)", got, explicit)
	}
}

// traceIDPtr returns a pointer to a stable traceID literal for the
// InsertOperatorIntent call site. The address cannot be taken on
// a const directly (untyped string constant); this helper holds the
// literal in a package-level var so traceIDPtr() returns a stable
// pointer reused across tests without per-call allocation.
func traceIDPtr() *string {
	return &traceIDLiteral
}

var traceIDLiteral = "4bf92f3577b34da6a3ce929d0e0e4736"
