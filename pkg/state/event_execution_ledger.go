package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type executionLedgerRootKey struct{ id, created string }
type executionLedgerRoot struct {
	key                        executionLedgerRootKey
	account, app, subscription string
	created, accepted          time.Time
	outbox                     int64
}
type executionLedgerOutcomeKey struct {
	id, created string
	generation  int64
	attempt     int
	kind        string
}
type executionLedgerOutcome struct {
	root                  executionLedgerRoot
	finished, retainUntil time.Time
	outcome               string
	latency               float64
}

func ledgerRootKey(id string, created time.Time) executionLedgerRootKey {
	return executionLedgerRootKey{canonicalMemUUID(id), created.UTC().Format(time.RFC3339Nano)}
}
func invocationLedgerRoot(inv Invocation) executionLedgerRootKey {
	if inv.ReplayRootInvocationID != "" && inv.ReplayRootCreatedAt != nil {
		return ledgerRootKey(inv.ReplayRootInvocationID, *inv.ReplayRootCreatedAt)
	}
	return ledgerRootKey(inv.ID, inv.CreatedAt)
}

func associateEventExecutionRootTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, receipt *PublishedEventWork, p eventAdmissionPlan) error {
	row, err := q.AssociateEventExecutionRoot(ctx, tx, sqlc.AssociateEventExecutionRootParams{InvocationID: mustPgUUID(p.invocation.ID), SubscriptionID: mustPgUUID(p.recipient.ID), AccountID: mustPgUUID(p.recipient.AccountID), AppID: mustPgUUID(p.recipient.AppID), AcceptedAt: pgtypeFromTime(receipt.CreatedAt), OutboxID: receipt.ID, RetentionSeconds: int32(api.EventExecutionLedgerRetention / time.Second)})
	if err != nil {
		return err
	}
	if row.InvocationExists && !row.Associated {
		return ErrConflict
	}
	if !row.Associated {
		return nil
	}
	if err = q.CaptureAssociatedEventCompletion(ctx, tx, mustPgUUID(p.invocation.ID)); err != nil {
		return err
	}
	return q.CaptureAssociatedEventAttempts(ctx, tx, mustPgUUID(p.invocation.ID))
}

func (m *MemStore) associateEventExecutionRootLocked(receipt *PublishedEventWork, p eventAdmissionPlan) error {
	inv, ok := m.invocations[p.invocation.ID]
	if !ok {
		return nil
	} // A retained cancellation can reconcile a handoff without execution.
	key := ledgerRootKey(inv.ID, inv.CreatedAt)
	root := executionLedgerRoot{key: key, account: canonicalMemUUID(inv.AccountID), app: canonicalMemUUID(inv.AppID), subscription: canonicalMemUUID(p.recipient.ID), created: inv.CreatedAt, accepted: receipt.CreatedAt, outbox: receipt.ID}
	if prior, exists := m.eventExecutionRoots[key]; exists && (prior.account != root.account || prior.app != root.app || prior.subscription != root.subscription || !prior.created.Equal(root.created) || !prior.accepted.Equal(root.accepted) || prior.outbox != root.outbox) {
		return ErrConflict
	}
	if m.eventExecutionRoots == nil {
		m.eventExecutionRoots = map[executionLedgerRootKey]executionLedgerRoot{}
	}
	m.eventExecutionRoots[key] = root
	if _, ok = m.eventExecutionCoverage[root.subscription]; !ok {
		if m.eventExecutionCoverage == nil {
			m.eventExecutionCoverage = map[string]executionCoverage{}
		}
		now := time.Now().UTC()
		m.eventExecutionCoverage[root.subscription] = executionCoverage{app: root.app, started: now, ledgerStarted: now}
	}
	m.captureEventExecutionTransitionLocked(Invocation{}, inv, time.Now().UTC(), false)
	for _, h := range m.invocationAttemptHistory {
		child, exists := m.invocations[h.InvocationID]
		if exists && h.FinishedAt != nil && !h.StartedAt.Before(child.CreatedAt) && !h.FinishedAt.Before(child.CreatedAt) && h.ReplayGeneration == child.ReplayGeneration && sameMemUUID(h.rootID, inv.ID) && h.rootCreatedAt.Equal(inv.CreatedAt) && h.RetainUntil.After(time.Now()) {
			m.appendEventExecutionOutcomeLocked(child, h.ReplayGeneration, h.Attempt, "attempt", *h.FinishedAt, h.Outcome)
		}
	}
	return nil
}

func (m *MemStore) appendEventExecutionOutcomeLocked(inv Invocation, generation int64, attempt int, kind string, finished time.Time, outcome string) {
	root, ok := m.eventExecutionRoots[invocationLedgerRoot(inv)]
	if !ok || !sameMemUUID(root.account, inv.AccountID) || !sameMemUUID(root.app, inv.AppID) {
		return
	}
	key := executionLedgerOutcomeKey{canonicalMemUUID(inv.ID), inv.CreatedAt.UTC().Format(time.RFC3339Nano), generation, attempt, kind}
	if _, exists := m.eventExecutionOutcomes[key]; exists {
		return
	}
	if finished.Before(root.created) || finished.Before(root.accepted) {
		floor := time.Now().UTC()
		if root.created.After(floor) {
			floor = root.created
		}
		if root.accepted.After(floor) {
			floor = root.accepted
		}
		m.markEventExecutionLedgerLossLocked(root.subscription, floor)
		return
	}
	if m.eventExecutionOutcomes == nil {
		m.eventExecutionOutcomes = map[executionLedgerOutcomeKey]executionLedgerOutcome{}
	}
	latency := 0.0
	if kind == "completion" {
		latency = finished.Sub(root.accepted).Seconds()
	}
	m.eventExecutionOutcomes[key] = executionLedgerOutcome{root: root, finished: finished, retainUntil: finished.Add(api.EventExecutionLedgerRetention), outcome: outcome, latency: latency}
}

func (m *MemStore) captureEventExecutionTransitionLocked(old, inv Invocation, now time.Time, deleted bool) {
	if old.ID != "" && old.State == InvocationDispatching && old.Attempts > 0 && (deleted || inv.State != InvocationDispatching || old.Attempts != inv.Attempts || old.ReplayGeneration != inv.ReplayGeneration) {
		outcome := "unknown"
		if !deleted {
			outcome = invocationAttemptOutcome(old, inv)
		}
		m.appendEventExecutionOutcomeLocked(old, old.ReplayGeneration, old.Attempts, "attempt", now, outcome)
	}
	if !deleted && inv.State == InvocationCompleted && inv.CompletedAt != nil {
		m.appendEventExecutionOutcomeLocked(inv, inv.ReplayGeneration, 0, "completion", *inv.CompletedAt, "succeeded")
	}
}

func (m *MemStore) markEventExecutionLedgerLossLocked(sub string, at time.Time) {
	id := canonicalMemUUID(sub)
	c, ok := m.eventExecutionCoverage[id]
	if !ok || (c.ledgerLostThrough != nil && !at.After(*c.ledgerLostThrough)) {
		return
	}
	c.ledgerLostThrough = cloneEventReceiptTime(&at)
	m.eventExecutionCoverage[id] = c
}

func observeEventExecutionLedgerWindow(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, out *api.EventConsumerExecutionHealth, account string) error {
	row, err := q.EventExecutionLedgerWindow(ctx, tx, sqlc.EventExecutionLedgerWindowParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(out.AppID), SubscriptionID: mustPgUUID(out.SubscriptionID), SinceAt: pgtypeFromTime(out.WindowStart), NowAt: pgtypeFromTime(out.ObservedAt)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	stats := executionLedgerStats{started: timestamptzToTimePtr(row.LedgerStartedAt), known: row.LedgerBaselineKnown, lost: timestamptzToTimePtr(row.LedgerLostThroughAt), expired: timestamptzToTimePtr(row.ExpiredThroughAt), retain: timestamptzToTimePtr(row.RetainUntil), success: row.SuccessfulAttempts, failed: row.FailedAttempts, unknown: row.UnknownAttempts, dead: row.DeadLetters, completions: row.Completions, p95: row.CompletionLatencyP95Seconds}
	applyExecutionLedgerStats(out, stats)
	return nil
}
