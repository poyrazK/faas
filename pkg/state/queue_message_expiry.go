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
// first. The wait counts from the message's send or, once replayed, from its
// last replay (ADR-20261101093812). A message a worker holds (dispatching)
// does not expire. It reports how many it moved.
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

// queueMessageWaitStart is when a pending message's retention started: its
// send, its last replay from the dead letter, or when a retry or nack delay
// last made it due (ADR-20261112200000), whichever is latest.
func queueMessageWaitStart(inv Invocation) time.Time {
	start := inv.CreatedAt
	if inv.LastReplayedAt != nil && inv.LastReplayedAt.After(start) {
		start = *inv.LastReplayedAt
	}
	if inv.DueAt.After(start) {
		start = inv.DueAt
	}
	return start
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

// invocationOwnerHeldLocked mirrors the expiry sweep's hold: a suspended or
// abuse-held owner's pending queue messages wait for the hold to lift.
func (m *MemStore) invocationOwnerHeldLocked(ownerID string) bool {
	if account, ok := m.accounts[ownerID]; ok {
		return account.Status == AccountSuspended || account.AbuseHoldAt != nil
	}
	if org, ok := m.orgs[ownerID]; ok {
		return org.Status == OrgStatusSuspended || org.AbuseHoldAt != nil
	}
	return false
}

// expireQueueReceiptsLocked mirrors the sweep's receipt update: an expired
// message's open push receipts move to dead_letter with it.
func (m *MemStore) expireQueueReceiptsLocked(inv Invocation) {
	for id, r := range m.records {
		t, ok := m.triggers[r.TriggerID.String()]
		if !ok || r.ItemIdentifier != inv.ID || t.Kind != "queue" || !t.Source.Valid || t.Source.String != string(InvocationQueue) ||
			!sameMemUUID(t.AppID.String(), inv.AppID) || (r.State != "pending" && r.State != "retry" && r.State != "claimed") {
			continue
		}
		r.State, r.ClaimExpiresAt = "dead_letter", pgtype.Timestamptz{}
		r.LastError = pgtype.Text{String: queueMessageExpiredError, Valid: true}
		m.records[id] = r
	}
}

func (m *MemStore) ExpirePendingQueueMessages(_ context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Invocation
	held := map[string]bool{}
	for _, inv := range m.invocations {
		if inv.Source != InvocationQueue || inv.State != InvocationPending {
			continue
		}
		plan, ok := m.invocationOwnerPlanLocked(inv.AccountID)
		if !ok {
			continue
		}
		retention, ok := queueMessageRetention(plan)
		if !ok {
			continue
		}
		if m.invocationOwnerHeldLocked(inv.AccountID) {
			// A held owner's message restarts its wait at half the retention.
			retention /= 2
			held[inv.ID] = true
		}
		if queueMessageWaitStart(inv).Before(now.Add(-retention)) {
			due = append(due, inv)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	if len(due) > limit {
		due = due[:limit]
	}
	settled := time.Now().UTC()
	outcome := OutcomeDeadLetter
	expired := 0
	for _, inv := range due {
		if held[inv.ID] {
			inv.DueAt = now
			m.setInvocationLocked(inv.ID, inv)
			continue
		}
		inv.State = InvocationDeadLetter
		inv.Outcome = &outcome
		inv.CompletedAt = &settled
		inv.LastError = queueMessageExpiredError
		inv.OutcomeCode = QueueMessageExpiredCode
		m.setInvocationLocked(inv.ID, inv)
		m.expireQueueReceiptsLocked(inv)
		expired++
	}
	return expired, nil
}
