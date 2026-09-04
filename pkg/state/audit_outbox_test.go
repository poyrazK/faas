package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemAuditEventOutboxDedupeAndDelivery(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	subject := "app-1"
	payload := []byte(`{"kind":"app.signature_invalid","app_id":"app-1"}`)

	first, err := store.EnqueueAuditEvent(ctx, "imaged", "app.signature_invalid", &subject, payload, "signature:dep-1")
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	payload[0] = 'X'
	second, err := store.EnqueueAuditEvent(ctx, "imaged", "app.signature_invalid", &subject, []byte(`{"different":true}`), "signature:dep-1")
	if err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	if second != first {
		t.Fatalf("duplicate enqueue id = %d, want stable id %d", second, first)
	}

	claimed, err := store.ClaimAuditEvent(ctx, "apid", time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != first || claimed.Attempts != 1 {
		t.Fatalf("claimed item = %+v, want id=%d attempts=1", claimed, first)
	}
	if _, err := store.ClaimAuditEvent(ctx, "apid", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second claim error = %v, want ErrNotFound", err)
	}

	delivered, err := store.DeliverAuditEvent(ctx, first)
	if err != nil || !delivered {
		t.Fatalf("deliver = (%v, %v), want (true, nil)", delivered, err)
	}
	delivered, err = store.DeliverAuditEvent(ctx, first)
	if err != nil || delivered {
		t.Fatalf("redeliver = (%v, %v), want (false, nil)", delivered, err)
	}
	events, err := store.ListEvents(ctx, "", 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events length = %d, want 1", len(events))
	}
	if events[0].Kind != "app.signature_invalid" || string(events[0].Data) != `{"kind":"app.signature_invalid","app_id":"app-1"}` {
		t.Fatalf("delivered event = %+v, want original payload", events[0])
	}
}

func TestMemAuditEventOutboxLeaseRecoveryAndDeadLetter(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	id, err := store.EnqueueAuditEvent(ctx, "imaged", "app.signature_missing", nil, []byte(`{}`), "signature:dep-2")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := store.ClaimAuditEvent(ctx, "apid-1", time.Minute); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	store.mu.Lock()
	row := store.auditOutbox[id]
	row.leaseUntil = time.Now().UTC().Add(-time.Second)
	store.auditOutbox[id] = row
	store.mu.Unlock()
	claimed, err := store.ClaimAuditEvent(ctx, "apid-2", time.Minute)
	if err != nil {
		t.Fatalf("recovery claim: %v", err)
	}
	if claimed.Attempts != 2 {
		t.Fatalf("recovery attempts = %d, want 2", claimed.Attempts)
	}

	store.mu.Lock()
	row = store.auditOutbox[id]
	row.Attempts = AuditEventOutboxMaxAttempts
	store.auditOutbox[id] = row
	store.mu.Unlock()
	if err := store.FailAuditEvent(ctx, id, errors.New("permanent test failure")); err != nil {
		t.Fatalf("dead-letter failure: %v", err)
	}
	store.mu.Lock()
	if got := store.auditOutbox[id].state; got != auditEventOutboxStateDeadLetter {
		t.Errorf("state after max attempts = %q, want %q", got, auditEventOutboxStateDeadLetter)
	}
	row = store.auditOutbox[id]
	row.createdAt = time.Now().UTC().Add(-time.Hour)
	store.auditOutbox[id] = row
	store.mu.Unlock()
	removed, err := store.PruneAuditEventOutbox(ctx, time.Now().UTC())
	if err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v), want (1, nil)", removed, err)
	}
	if _, err := store.DeliverAuditEvent(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deliver after prune error = %v, want ErrNotFound", err)
	}
}
