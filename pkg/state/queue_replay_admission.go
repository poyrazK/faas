package state

// adr: 20261101093812 — replayed queue messages meet the queue caps and get
// a fresh retention; lapsed last-attempt leases dead-letter.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// QueueLeaseAttemptsExhaustedCode is the outcome code of a pull-leased queue
// message dead-lettered because its leases lapsed until its attempt budget
// ran out.
const QueueLeaseAttemptsExhaustedCode = "queue_lease_attempts_exhausted"

const queueLeaseExhaustedError = "queue lease attempt budget exhausted after lease expiry"

// IsQueueCapError reports whether err is a queue cap refusing a message: its
// app at the plan's queue depth or its account at the plan's queue backlog
// bytes. A replay of many dead letters skips a message it refuses.
func IsQueueCapError(err error) bool {
	var depth *InvocationDepthError
	var backlog *InvocationBacklogBytesError
	return errors.As(err, &depth) || errors.As(err, &backlog)
}

// KeyedReplayExpired reports whether inv is keyed work whose pending deadline
// has passed (ADR-20261031101000). Replay resets its attempts, so it would
// only expire again; a replay of many dead letters skips it.
func KeyedReplayExpired(inv Invocation, now time.Time) bool {
	return inv.WorkPolicyName != "" && inv.WorkExpiresAt != nil && !inv.WorkExpiresAt.After(now)
}

// queueReplayAdmission is the admission a replayed production queue message
// meets: its plan's per-app queue depth and per-account backlog bytes, the
// caps a send meets.
func queueReplayAdmission(appID, accountID string, plan api.Plan) (Invocation, bool) {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return Invocation{}, false
	}
	return Invocation{AppID: appID, AccountID: accountID, Source: InvocationQueue,
		AdmissionMaxPending: limits.MaxQueueDepth, AdmissionMaxBacklogBytes: limits.MaxQueueBacklogBytesPerAccount}, true
}

// checkQueueReplayAdmissionTx refuses to replay a dead-lettered production
// queue message while its app is at its plan's queue depth or its account at
// its queue backlog bytes, with the errors a send gets. It counts without the
// app lock a send takes, so a replay racing a send to the same app may pass
// the depth cap by the messages in flight. Any other row passes.
func checkQueueReplayAdmissionTx(ctx context.Context, tx pgx.Tx, id pgtype.UUID) error {
	row, err := sqlc.New().ReadQueueReplayAdmission(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("state: queue replay admission: %w", err)
	}
	admission, ok := queueReplayAdmission(uuidString(row.AppID), uuidString(row.AccountID), api.Plan(row.Plan))
	if !ok {
		return nil
	}
	if err := checkInvocationAdmissionDepthDB(ctx, tx, admission); err != nil {
		if IsQueueCapError(err) {
			return err
		}
		return fmt.Errorf("state: queue replay admission count: %w", err)
	}
	return nil
}

// checkQueueReplayAdmissionLocked mirrors checkQueueReplayAdmissionTx.
func (m *MemStore) checkQueueReplayAdmissionLocked(inv Invocation) error {
	if inv.Source != InvocationQueue || !m.productionInvocationWorkLocked(inv) {
		return nil
	}
	plan, ok := m.invocationOwnerPlanLocked(inv.AccountID)
	if !ok {
		return nil
	}
	admission, ok := queueReplayAdmission(inv.AppID, inv.AccountID, plan)
	if !ok {
		return nil
	}
	return m.checkInvocationAdmissionLocked(admission, nil)
}

// queueLeaseAttemptsExhausted reports whether a pull-leased message has no
// attempt left under its retry policy and its owner's plan.
func queueLeaseAttemptsExhausted(attempts int, retryPolicy json.RawMessage, plan api.Plan) bool {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return false
	}
	return environmentQueueDeliveryAttemptsExhausted(Invocation{Attempts: attempts, RetryPolicyJSON: retryPolicy}, limits)
}

// exhaustExpiredQueueLeasesTx dead-letters pull leases that lapsed on their
// message's last attempt. Returned to pending, such a message waited for a
// lease call to dead-letter it, and with no consumer it expired after the
// plan's retention under a misleading code. RequeueExpiredInvocations returns
// the other lapsed leases to pending after it.
func exhaustExpiredQueueLeasesTx(ctx context.Context, tx pgx.Tx, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	q := sqlc.New()
	at := pgtype.Timestamptz{Time: now.UTC(), Valid: true}
	rows, err := q.ExpiredQueueLeases(ctx, tx, sqlc.ExpiredQueueLeasesParams{NowAt: at, BatchLimit: int32(limit)})
	if err != nil {
		return 0, fmt.Errorf("state: expired queue leases: %w", err)
	}
	exhausted := 0
	for _, row := range rows {
		if !queueLeaseAttemptsExhausted(int(row.Attempts), row.RetryPolicy, api.Plan(row.Plan)) {
			continue
		}
		n, err := q.ExhaustExpiredQueueLease(ctx, tx, sqlc.ExhaustExpiredQueueLeaseParams{ID: row.ID, Attempt: row.Attempts, NowAt: at})
		if err != nil {
			return 0, fmt.Errorf("state: exhaust expired queue lease: %w", err)
		}
		exhausted += int(n)
	}
	return exhausted, nil
}

// queueLeaseExhaustedLocked mirrors ExpiredQueueLeases and the budget check:
// inv holds a lapsed pull lease on its last attempt.
func (m *MemStore) queueLeaseExhaustedLocked(inv Invocation) bool {
	if inv.Source != InvocationQueue || inv.EnvironmentID != "" || inv.QuotaReserved {
		return false
	}
	receipt, ok := m.invocationQueueLeaseReceipts[inv.ID]
	if !ok || receipt.Attempt != inv.Attempts || receipt.ReplayGeneration != inv.ReplayGeneration {
		return false
	}
	plan, ok := m.invocationOwnerPlanLocked(inv.AccountID)
	return ok && queueLeaseAttemptsExhausted(inv.Attempts, inv.RetryPolicyJSON, plan)
}

// exhaustQueueLease dead-letters a pull-leased message whose leases spent its
// attempt budget.
func exhaustQueueLease(inv Invocation, now time.Time) Invocation {
	inv = exhaustEnvironmentQueueDelivery(inv, now)
	inv.LastError = queueLeaseExhaustedError
	inv.OutcomeCode = QueueLeaseAttemptsExhaustedCode
	return inv
}
