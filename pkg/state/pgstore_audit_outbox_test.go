package state_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStoreAuditEventOutboxRoundTrip(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	subject := "00000000-0000-0000-0000-000000000140"
	payload := []byte(`{"app_id":"` + subject + `"}`)

	id, err := s.EnqueueAuditEvent(ctx, "apid", "app.signature_invalid", &subject, payload, "signature:pg-dep-1")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	duplicateID, err := s.EnqueueAuditEvent(ctx, "apid", "app.signature_invalid", &subject, []byte(`{"replacement":true}`), "signature:pg-dep-1")
	if err != nil {
		t.Fatalf("duplicate enqueue: %v", err)
	}
	if duplicateID != id {
		t.Fatalf("duplicate id = %d, want %d", duplicateID, id)
	}

	claimed, err := s.ClaimAuditEvent(ctx, "apid-test", time.Minute)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if claimed.ID != id || claimed.Attempts != 1 || claimed.Subject == nil || *claimed.Subject != subject {
		t.Fatalf("claimed item = %+v, want id=%d attempts=1 subject=%s", claimed, id, subject)
	}

	delivered, err := s.DeliverAuditEvent(ctx, id)
	if err != nil || !delivered {
		t.Fatalf("deliver = (%v, %v), want (true, nil)", delivered, err)
	}
	delivered, err = s.DeliverAuditEvent(ctx, id)
	if err != nil || delivered {
		t.Fatalf("redeliver = (%v, %v), want (false, nil)", delivered, err)
	}
	events, err := s.ListEvents(ctx, subject, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var gotData, wantData map[string]string
	if len(events) == 1 {
		if err := json.Unmarshal(events[0].Data, &gotData); err != nil {
			t.Fatalf("decode delivered event data: %v", err)
		}
		if err := json.Unmarshal(payload, &wantData); err != nil {
			t.Fatalf("decode expected event data: %v", err)
		}
	}
	if len(events) != 1 || events[0].Kind != "app.signature_invalid" || len(gotData) != len(wantData) || gotData["app_id"] != wantData["app_id"] {
		t.Fatalf("events = %+v, want one original audit event", events)
	}

	removed, err := s.PruneAuditEventOutbox(ctx, time.Now().UTC().Add(time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("prune = (%d, %v), want (1, nil)", removed, err)
	}
	var outboxID *int64
	if err := pool.QueryRow(ctx, `select outbox_id from events where id = $1`, events[0].ID).Scan(&outboxID); err != nil {
		t.Fatalf("read pruned event FK: %v", err)
	}
	if outboxID != nil {
		t.Errorf("events.outbox_id after queue prune = %v, want NULL", *outboxID)
	}
	if _, err := s.DeliverAuditEvent(ctx, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deliver after prune = %v, want ErrNotFound", err)
	}

	retryID, err := s.EnqueueAuditEvent(ctx, "apid", "app.signature_missing", &subject, []byte(`{}`), "signature:pg-dep-2")
	if err != nil {
		t.Fatalf("retry enqueue: %v", err)
	}
	if _, err := s.ClaimAuditEvent(ctx, "apid-test", time.Minute); err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if err := s.FailAuditEvent(ctx, retryID, errors.New("temporary registry error")); err != nil {
		t.Fatalf("fail for retry: %v", err)
	}
	var retryState string
	if err := pool.QueryRow(ctx, `select state from audit_event_outbox where id = $1`, retryID).Scan(&retryState); err != nil {
		t.Fatalf("read retry state: %v", err)
	}
	if retryState != "pending" {
		t.Fatalf("retry state = %q, want pending", retryState)
	}
	if _, err := pool.Exec(ctx, `update audit_event_outbox set available_at = now() - interval '1 second' where id = $1`, retryID); err != nil {
		t.Fatalf("make retry due: %v", err)
	}
	claimed, err = s.ClaimAuditEvent(ctx, "apid-test", time.Minute)
	if err != nil || claimed.ID != retryID || claimed.Attempts != 2 {
		t.Fatalf("retry claim = (%+v, %v), want id=%d attempts=2", claimed, err, retryID)
	}
	if _, err := pool.Exec(ctx, `update audit_event_outbox set attempts = $2 where id = $1`, retryID, state.AuditEventOutboxMaxAttempts); err != nil {
		t.Fatalf("set max attempts: %v", err)
	}
	if err := s.FailAuditEvent(ctx, retryID, errors.New("permanent registry error")); err != nil {
		t.Fatalf("dead-letter retry: %v", err)
	}
	if err := pool.QueryRow(ctx, `select state from audit_event_outbox where id = $1`, retryID).Scan(&retryState); err != nil {
		t.Fatalf("read dead-letter state: %v", err)
	}
	if retryState != "dead_letter" {
		t.Fatalf("dead-letter state = %q, want dead_letter", retryState)
	}
}
