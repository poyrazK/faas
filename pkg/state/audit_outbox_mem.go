package state

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var _ AuditEventOutboxStore = (*MemStore)(nil)

// EnqueueAuditEvent appends one durable audit handoff to the in-memory queue.
// The dedupe key mirrors the production unique constraint and makes retries at
// the imaged call site safe.
func (m *MemStore) EnqueueAuditEvent(_ context.Context, actor, kind string, subject *string, data []byte, dedupeKey string) (int64, error) {
	if err := validateAuditEventOutboxInput(actor, kind, data, dedupeKey); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.auditOutbox == nil {
		m.auditOutbox = make(map[int64]auditEventOutboxRow)
	}
	if m.auditOutboxByKey == nil {
		m.auditOutboxByKey = make(map[string]int64)
	}
	if id, ok := m.auditOutboxByKey[dedupeKey]; ok {
		return id, nil
	}
	if m.nextAuditOutboxID <= 0 {
		m.nextAuditOutboxID = 1
	}
	now := time.Now().UTC()
	item := AuditEventOutbox{
		ID:        m.nextAuditOutboxID,
		Actor:     actor,
		Kind:      kind,
		Subject:   cloneStringPtr(subject),
		Data:      append([]byte(nil), data...),
		DedupeKey: dedupeKey,
	}
	m.auditOutbox[item.ID] = auditEventOutboxRow{
		AuditEventOutbox: item,
		state:            auditEventOutboxStatePending,
		availableAt:      now,
		createdAt:        now,
	}
	m.auditOutboxByKey[dedupeKey] = item.ID
	m.nextAuditOutboxID++
	return item.ID, nil
}

// ClaimAuditEvent mirrors the Postgres FOR UPDATE SKIP LOCKED claim. A
// processing row whose lease expired is eligible for recovery by any worker.
func (m *MemStore) ClaimAuditEvent(_ context.Context, consumer string, lease time.Duration) (AuditEventOutbox, error) {
	if strings.TrimSpace(consumer) == "" {
		return AuditEventOutbox{}, errors.New("state: audit event outbox consumer required")
	}
	now := time.Now().UTC()
	lease = auditEventOutboxLease(lease)
	m.mu.Lock()
	defer m.mu.Unlock()
	var selected *auditEventOutboxRow
	var selectedID int64
	for id := range m.auditOutbox {
		row := m.auditOutbox[id]
		eligible := row.state == auditEventOutboxStatePending && !row.availableAt.After(now)
		if row.state == auditEventOutboxStateProcessing && !row.leaseUntil.After(now) {
			eligible = true
		}
		if !eligible || (selected != nil && id >= selectedID) {
			continue
		}
		copyRow := row
		selected = &copyRow
		selectedID = id
	}
	if selected == nil {
		return AuditEventOutbox{}, ErrNotFound
	}
	selected.state = auditEventOutboxStateProcessing
	selected.Attempts++
	selected.claimedBy = consumer
	selected.claimedAt = now
	selected.leaseUntil = now.Add(lease)
	m.auditOutbox[selectedID] = *selected
	return cloneAuditEventOutbox(selected.AuditEventOutbox), nil
}

// DeliverAuditEvent writes the audit row and marks its queue item delivered
// under the same in-memory critical section. A second delivery is a no-op,
// matching the events.outbox_id unique index in Postgres.
func (m *MemStore) DeliverAuditEvent(_ context.Context, id int64) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.auditOutbox[id]
	if !ok {
		return false, ErrNotFound
	}
	if row.state == auditEventOutboxStateDelivered || row.state == auditEventOutboxStateDeadLetter {
		return false, nil
	}
	if m.events == nil {
		m.events = []Event{}
	}
	var subject *uuid.UUID
	if row.Subject != nil {
		subject = parseSubjectID(*row.Subject)
	}
	m.events = append(m.events, Event{
		ID:      int64(len(m.events) + 1),
		At:      time.Now().UTC(),
		Actor:   row.Actor,
		Kind:    row.Kind,
		Subject: subject,
		Data:    append([]byte(nil), row.Data...),
	})
	now := time.Now().UTC()
	row.state = auditEventOutboxStateDelivered
	row.deliveredAt = now
	row.claimedBy = ""
	row.claimedAt = time.Time{}
	row.leaseUntil = time.Time{}
	row.lastError = ""
	m.auditOutbox[id] = row
	return true, nil
}

// FailAuditEvent releases a claimed row for retry, or moves it to the
// dead-letter state after the configured failure threshold.
func (m *MemStore) FailAuditEvent(_ context.Context, id int64, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.auditOutbox[id]
	if !ok {
		return ErrNotFound
	}
	if row.state == auditEventOutboxStateDelivered || row.state == auditEventOutboxStateDeadLetter {
		return nil
	}
	message := auditEventOutboxFailureMessage(cause)
	row.lastError = message
	row.claimedBy = ""
	row.claimedAt = time.Time{}
	row.leaseUntil = time.Time{}
	if row.Attempts >= AuditEventOutboxMaxAttempts {
		row.state = auditEventOutboxStateDeadLetter
		row.availableAt = time.Time{}
	} else {
		row.state = auditEventOutboxStatePending
		row.availableAt = time.Now().UTC().Add(auditEventOutboxRetryDelay(row.Attempts))
	}
	m.auditOutbox[id] = row
	return nil
}

// PruneAuditEventOutbox removes terminal queue metadata while retaining the
// events row. The production FK uses ON DELETE SET NULL for that same reason.
func (m *MemStore) PruneAuditEventOutbox(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var removed int64
	for id, row := range m.auditOutbox {
		if row.state != auditEventOutboxStateDelivered && row.state != auditEventOutboxStateDeadLetter {
			continue
		}
		at := row.createdAt
		if !row.deliveredAt.IsZero() {
			at = row.deliveredAt
		}
		if at.IsZero() || !at.Before(before) {
			continue
		}
		delete(m.auditOutbox, id)
		delete(m.auditOutboxByKey, row.DedupeKey)
		removed++
	}
	return removed, nil
}

func cloneStringPtr(in *string) *string {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}
