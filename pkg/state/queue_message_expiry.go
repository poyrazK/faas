package state

// adr: 20261022105723 — queue backlog and retention per plan.

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// QueueMessageExpiryStore moves pending queue messages that waited longer
// than their plan's QueueMessageRetentionSeconds to the dead letter, oldest
// first. A message a worker holds (dispatching) does not expire. It reports
// how many it moved.
type QueueMessageExpiryStore interface {
	ExpirePendingQueueMessages(ctx context.Context, now time.Time, limit int) (int, error)
}

// QueueMessageExpiredCode is the outcome code of an expired queue message.
const QueueMessageExpiredCode = "queue_message_expired"

const queueMessageExpiredError = "queue message expired: it waited longer than the plan retention"

// queueMessageRetention is a plan's pending-message retention, or false
// when the plan has none.
func queueMessageRetention(plan api.Plan) (time.Duration, bool) {
	limits, ok := api.LimitsFor(plan)
	if !ok || limits.QueueMessageRetentionSeconds <= 0 {
		return 0, false
	}
	return time.Duration(limits.QueueMessageRetentionSeconds) * time.Second, true
}

func (s *PgStore) ExpirePendingQueueMessages(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	params := sqlc.ExpirePendingQueueMessagesParams{NowAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true}, BatchLimit: int32(limit)}
	for _, plan := range api.Plans {
		retention, ok := queueMessageRetention(plan)
		if !ok {
			continue
		}
		seconds := int64(retention / time.Second)
		params.Plans = append(params.Plans, string(plan))
		params.RetentionSeconds = append(params.RetentionSeconds, seconds)
		if params.MinRetentionSeconds == 0 || seconds < params.MinRetentionSeconds {
			params.MinRetentionSeconds = seconds
		}
	}
	if len(params.Plans) == 0 {
		return 0, nil
	}
	rows, err := sqlc.New().ExpirePendingQueueMessages(ctx, s.pool, params)
	return len(rows), err
}

func (m *MemStore) ExpirePendingQueueMessages(_ context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Invocation
	for _, inv := range m.invocations {
		if inv.Source != InvocationQueue || inv.State != InvocationPending {
			continue
		}
		plan, ok := m.invocationOwnerPlanLocked(inv.AccountID)
		if !ok {
			continue
		}
		if retention, ok := queueMessageRetention(plan); ok && inv.CreatedAt.Before(now.Add(-retention)) {
			due = append(due, inv)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	settled := time.Now().UTC()
	outcome := OutcomeDeadLetter
	for _, inv := range due {
		inv.State = InvocationDeadLetter
		inv.Outcome = &outcome
		inv.CompletedAt = &settled
		inv.LastError = queueMessageExpiredError
		inv.OutcomeCode = QueueMessageExpiredCode
		m.setInvocationLocked(inv.ID, inv)
	}
	return len(due), nil
}
