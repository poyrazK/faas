// store_trace_id_test.go — PR-#TBD / C2 — pins the trace_id
// round-trip contract for MemStore.AppendEventWithTrace and
// MemStore.InsertOperatorIntent. The PgStore equivalents are
// covered by pgstore_operator_intent_test.go (and
// migrations/00486_events_operator_intents_trace_id_test.go
// for the schema).
//
// Pins:
//
//  1. AppendEventWithTrace stores TraceID on the resulting Event row.
//  2. AppendEvent (the shim) leaves TraceID nil when called without
//     a trace_id — preserves pre-PR contract for callers that don't
//     know about trace_ids.
//  3. AppendEventWithTrace rejects a non-OTel-hex trace_id (the
//     migration CHECK at 00486 enforces the same regex on
//     events.trace_id for PgStore; MemStore validates defensively
//     at the boundary so test doubles cannot accept an invalid
//     value).
//  4. InsertOperatorIntent stores the trace_id on the resulting
//     OperatorIntent row.
//  5. ClaimPendingOperatorIntent round-trips the trace_id (i.e.
//     the value persists through the FOR UPDATE SKIP LOCKED claim).
//  6. GetOperatorIntent round-trips the trace_id.
//  7. InsertOperatorIntent rejects a non-OTel-hex trace_id with
//     a clear error (mirrors 00486's CHECK violation contract).

package state

import (
	"context"
	"strings"
	"testing"
)

// TestMemStore_AppendEventWithTrace_StoresTraceID is the in-memory
// twin of the production AppendEventWithTrace contract.
func TestMemStore_AppendEventWithTrace_StoresTraceID(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"

	if err := m.AppendEventWithTrace(ctx, "test", "kind", nil, []byte(`{"k":"v"}`), &traceID); err != nil {
		t.Fatalf("AppendEventWithTrace: %v", err)
	}

	// Read-back via ListEvents; the existing ListEvents SELECT
	// doesn't include trace_id, so the field is the zero value
	// (nil) for this read. We need a direct introspection path:
	// the package-private m.events slice.
	m.mu.Lock()
	if len(m.events) != 1 {
		m.mu.Unlock()
		t.Fatalf("events length = %d, want 1", len(m.events))
	}
	got := m.events[0].TraceID
	m.mu.Unlock()

	if got == nil {
		t.Fatalf("Event.TraceID is nil; want %q", traceID)
	}
	if *got != traceID {
		t.Errorf("Event.TraceID = %q, want %q", *got, traceID)
	}
}

// TestMemStore_AppendEvent_ShimLeavesTraceIDNil confirms the
// four-arg shim still works without trace_id propagation.
func TestMemStore_AppendEvent_ShimLeavesTraceIDNil(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if err := m.AppendEvent(ctx, "test", "kind", nil, []byte(`{}`)); err != nil {
		t.Fatalf("AppendEvent shim: %v", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) != 1 {
		t.Fatalf("events length = %d, want 1", len(m.events))
	}
	if m.events[0].TraceID != nil {
		t.Errorf("Event.TraceID = %v, want nil for AppendEvent shim", *m.events[0].TraceID)
	}
}

// TestMemStore_AppendEventWithTrace_RejectsNonOTelHex pins the
// defensive validation in MemStore that mirrors the migration
// CHECK constraint at 00486.
func TestMemStore_AppendEventWithTrace_RejectsNonOTelHex(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	for _, bad := range []string{
		"",                                  // empty
		"not-hex",                           // non-hex chars
		"4bf92f3577b34da6a3ce929d0e0e473",   // 31 chars (too short)
		"4bf92f3577b34da6a3ce929d0e0e47360", // 33 chars (too long)
		"4BF92F3577B34DA6A3CE929D0E0E4736",  // uppercase (CHECK is lowercase only)
	} {
		badCopy := bad
		err := m.AppendEventWithTrace(ctx, "test", "kind", nil, []byte(`{}`), &badCopy)
		if err == nil {
			t.Errorf("AppendEventWithTrace(trace_id=%q) accepted; want error", bad)
		} else if !strings.Contains(err.Error(), "trace_id") || !strings.Contains(err.Error(), "^[0-9a-f]{32}$") {
			t.Errorf("AppendEventWithTrace(trace_id=%q) error = %v; want message mentioning trace_id + OTel regex", bad, err)
		}
	}
}

// TestMemStore_InsertOperatorIntent_StoresTraceID pins the new
// traceID argument on the operator_intents insert path.
func TestMemStore_InsertOperatorIntent_StoresTraceID(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"

	id, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForcePark, "target-id", nil, "actor", "test", nil, &traceID)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	got, err := m.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got.TraceID == nil {
		t.Fatalf("OperatorIntent.TraceID is nil; want %q", traceID)
	}
	if *got.TraceID != traceID {
		t.Errorf("OperatorIntent.TraceID = %q, want %q", *got.TraceID, traceID)
	}
}

// TestMemStore_TraceLookupExactAndBounded pins the operator diagnostic read
// contract: unrelated traces never leak and the newest matching rows survive
// the caller-provided bound.
func TestMemStore_TraceLookupExactAndBounded(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
	otherTraceID := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	if _, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForcePark, "target-a", nil,
		"actor", "first", nil, &traceID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForcePark, "target-b", nil,
		"actor", "unrelated", nil, &otherTraceID); err != nil {
		t.Fatal(err)
	}
	intents, err := m.ListOperatorIntentsByTraceID(ctx, traceID, 10)
	if err != nil || len(intents) != 1 || intents[0].Reason != "first" {
		t.Fatalf("ListOperatorIntentsByTraceID = (%v, %v), want one exact match", intents, err)
	}

	if err := m.AppendEventWithTrace(ctx, "apid", "operator.action.started", nil, []byte(`{"n":1}`), &traceID); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendEventWithTrace(ctx, "apid", "operator.action.unrelated", nil, []byte(`{}`), &otherTraceID); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendEventWithTrace(ctx, "schedd", "operator.action.completed", nil, []byte(`{"n":2}`), &traceID); err != nil {
		t.Fatal(err)
	}
	events, err := m.ListEventsByTraceID(ctx, traceID, 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("ListEventsByTraceID = (%v, %v), want one bounded match", events, err)
	}
	if events[0].Kind != "operator.action.completed" || events[0].TraceID == nil || *events[0].TraceID != traceID {
		t.Fatalf("latest event = %+v, want completed event with trace id", events[0])
	}
}

// TestMemStore_InsertOperatorIntent_NilTraceIDAllowsNil confirms
// the pre-PR contract (no trace_id) still works.
func TestMemStore_InsertOperatorIntent_NilTraceIDAllowsNil(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	id, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForcePark, "target-id", nil, "actor", "test", nil, nil)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	got, err := m.GetOperatorIntent(ctx, id)
	if err != nil {
		t.Fatalf("GetOperatorIntent: %v", err)
	}
	if got.TraceID != nil {
		t.Errorf("OperatorIntent.TraceID = %v, want nil for nil traceID arg", *got.TraceID)
	}
}

// TestMemStore_ClaimPendingOperatorIntent_RoundTripsTraceID pins
// the FOR UPDATE SKIP LOCKED claim path preserves the trace_id.
func TestMemStore_ClaimPendingOperatorIntent_RoundTripsTraceID(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	traceID := "4bf92f3577b34da6a3ce929d0e0e4736"

	id, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForceRestart, "instance-id", nil, "actor", "test", nil, &traceID)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	claimed, err := m.ClaimPendingOperatorIntent(ctx)
	if err != nil {
		t.Fatalf("ClaimPendingOperatorIntent: %v", err)
	}
	if claimed.ID != id {
		t.Fatalf("ClaimPendingOperatorIntent returned id=%q; want %q", claimed.ID, id)
	}
	if claimed.TraceID == nil {
		t.Fatalf("Claimed.TraceID is nil; want %q", traceID)
	}
	if *claimed.TraceID != traceID {
		t.Errorf("Claimed.TraceID = %q, want %q", *claimed.TraceID, traceID)
	}
}

// TestMemStore_InsertOperatorIntent_RejectsNonOTelHex pins the
// defensive validation on the operator_intents path. Mirrors
// the migration CHECK at 00486 for events.operator_intents.
func TestMemStore_InsertOperatorIntent_RejectsNonOTelHex(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	bad := "not-hex"
	_, err := m.InsertOperatorIntent(ctx, OperatorIntentKindForcePark, "target-id", nil, "actor", "test", nil, &bad)
	if err == nil {
		t.Fatalf("InsertOperatorIntent(trace_id=%q) accepted; want error", bad)
	}
	if !strings.Contains(err.Error(), "trace_id") {
		t.Errorf("InsertOperatorIntent error = %v; want message mentioning trace_id", err)
	}
}

// TestIsOTelHex32 pins the defensive validator used by
// AppendEventWithTrace + InsertOperatorIntent in MemStore.
// Covered by the two Rejects* tests above end-to-end, but the
// boundary cases (len, hex alphabet) are pinned here directly
// so a future refactor of isOTelHex32 cannot regress silently.
func TestIsOTelHex32(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"4bf92f3577b34da6a3ce929d0e0e4736", true},
		{"00000000000000000000000000000000", true},
		{"ffffffffffffffffffffffffffffffff", true},
		{"", false},
		{"4bf92f3577b34da6a3ce929d0e0e473", false},   // 31
		{"4bf92f3577b34da6a3ce929d0e0e47360", false}, // 33
		{"4BF92F3577B34DA6A3CE929D0E0E4736", false},  // uppercase
		{"4bf92f3577b34da6a3ce929d0e0e473g", false},  // non-hex char
	}
	for _, c := range cases {
		if got := isOTelHex32(c.in); got != c.want {
			t.Errorf("isOTelHex32(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
