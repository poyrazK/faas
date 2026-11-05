package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// FireNowRecoveryStore settles manual cron runs a scheduler restart
// interrupted. Claiming a request marks it running in its own write, and
// linking it to the run it queued is a later one: a scheduler that stopped
// between the two left the request running forever (crons audit 2026-11-02).
type FireNowRecoveryStore interface {
	// FailInterruptedFireNowRequests fails the running requests of nodeID's
	// apps (every app when nodeID is empty) that were requested before before
	// and never linked to an invocation, task or operation, with message.
	FailInterruptedFireNowRequests(ctx context.Context, nodeID string, before time.Time, message string) (int64, error)
}

// FailInterruptedFireNowRequests implements FireNowRecoveryStore.
func (s *PgStore) FailInterruptedFireNowRequests(ctx context.Context, nodeID string, before time.Time, message string) (int64, error) {
	n, err := sqlc.New().FailInterruptedFireNowRequests(ctx, s.pool, sqlc.FailInterruptedFireNowRequestsParams{
		Message: message,
		Before:  pgtype.Timestamptz{Time: before.UTC(), Valid: true},
		NodeID:  pgtype.Text{String: nodeID, Valid: nodeID != ""},
	})
	if err != nil {
		return 0, fmt.Errorf("state: fail interrupted fire-now requests: %w", err)
	}
	return n, nil
}

// FailInterruptedFireNowRequests implements FireNowRecoveryStore for the
// in-memory store, mirroring the PgStore query.
func (m *MemStore) FailInterruptedFireNowRequests(_ context.Context, nodeID string, before time.Time, message string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	var n int64
	for id, r := range m.fireNowRequests {
		if r.Status != FireNowStatusRunning || r.InvocationID != nil || r.TaskID != nil || r.OperationID != nil || !r.RequestedAt.Before(before) {
			continue
		}
		cron, ok := m.crons[r.CronID]
		if !ok {
			continue
		}
		app, ok := m.apps[cron.AppID]
		if !ok || nodeID != "" && app.NodeID != "" && app.NodeID != nodeID {
			continue
		}
		msg := message
		r.Status, r.Error, r.FinishedAt = FireNowStatusFailed, &msg, &now
		m.fireNowRequests[id] = r
		n++
	}
	return n, nil
}
