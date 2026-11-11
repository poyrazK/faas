package state

// adr: 20261018101500 — amendment 2026-10-31: completed queue payloads.

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TrimmedQueuePayload is the payload a completed queue message keeps once its
// payload retention has passed: JSON null, distinct from an empty object.
var TrimmedQueuePayload = json.RawMessage(`null`)

// QueuePayloadTrimStore clears the payload of queue messages that completed
// before completedBefore, oldest first, and reports how many it cleared. The
// message's history row stays for the plan's result retention; failures and
// dead letters keep their payloads for replay.
type QueuePayloadTrimStore interface {
	TrimCompletedQueuePayloads(ctx context.Context, completedBefore time.Time, limit int) (int, error)
}

var (
	_ QueuePayloadTrimStore = (*PgStore)(nil)
	_ QueuePayloadTrimStore = (*MemStore)(nil)
)

func (s *PgStore) TrimCompletedQueuePayloads(ctx context.Context, completedBefore time.Time, limit int) (int, error) {
	rows, err := queueDepthLimit(limit)
	if err != nil {
		return 0, err
	}
	n, err := sqlc.New().TrimCompletedQueuePayloads(ctx, s.pool, sqlc.TrimCompletedQueuePayloadsParams{
		CompletedBefore: pgtype.Timestamptz{Time: completedBefore.UTC(), Valid: true}, RowLimit: rows})
	if err != nil {
		return 0, mapErr(err)
	}
	return int(n), nil
}

func (m *MemStore) TrimCompletedQueuePayloads(_ context.Context, completedBefore time.Time, limit int) (int, error) {
	if _, err := queueDepthLimit(limit); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Invocation
	for _, inv := range m.invocations {
		if inv.Source == InvocationQueue && inv.State == InvocationCompleted && inv.CompletedAt != nil &&
			!inv.CompletedAt.After(completedBefore) && string(inv.Payload) != string(TrimmedQueuePayload) {
			due = append(due, inv)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CompletedAt.Before(*due[j].CompletedAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	for _, inv := range due {
		inv.Payload = append(json.RawMessage(nil), TrimmedQueuePayload...)
		m.setInvocationLocked(inv.ID, inv)
	}
	return len(due), nil
}
