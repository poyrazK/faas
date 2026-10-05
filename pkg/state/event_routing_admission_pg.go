package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func routingReceiptFromSQL(row sqlc.EventFanoutOutbox) (*PublishedEventWork, error) {
	receipt := &PublishedEventWork{ID: row.ID, Payload: row.Payload, SnapshotCaptured: row.RecipientSnapshot != nil,
		RecipientClaims: row.RecipientClaims, CreatedAt: timeFromPgtype(row.CreatedAt), Delivered: row.State == "delivered"}
	if receipt.SnapshotCaptured {
		if err := json.Unmarshal(row.RecipientSnapshot, &receipt.RecipientSnapshot); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(row.RecipientProgress, &receipt.RecipientProgress); err != nil {
		return nil, err
	}
	return receipt, nil
}

func (s *PgStore) AdmitPublishedEventRecipient(ctx context.Context, claim PublishedEventRoutingClaim) (result PublishedEventRoutingResult, err error) {
	// A storage failure can occur before filter matching or after a filtered
	// outcome. Preserve retry classification even when Matched is still false.
	defer func() {
		if err != nil && !errors.Is(err, ErrNotFound) {
			err = eventAdmissionPrepareError(err)
		}
	}()
	q := sqlc.New()
	row, err := q.EventRoutingReceipt(ctx, s.pool, claim.OutboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedEventRoutingResult{}, ErrNotFound
	}
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	receipt, err := routingReceiptFromSQL(row)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	plan, err := newEventAdmissionPlan(ctx, receipt, claim)
	if err != nil {
		return eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false), err
	}
	defer func() {
		if err != nil && result.AppID == "" {
			result = eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false)
		}
	}()
	if err := prepareEventAdmission(ctx, s, receipt, &plan); err != nil {
		return eventAdmissionResult(plan, PublishedEventRecipientProgress{}, false, false), eventAdmissionPrepareError(err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if plan.matched && !plan.prior && plan.invocation.WorkPolicyName != "" {
		if _, err := lockWorkAdmissionLane(ctx, tx, plan.invocation.AppID, plan.invocation.WorkPolicyName, plan.invocation.WorkKeyDigest); err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	return admitEventRecipientTx(ctx, q, tx, claim, plan)
}

func admitEventRecipientTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, claim PublishedEventRoutingClaim, p eventAdmissionPlan) (PublishedEventRoutingResult, error) {
	row, err := q.EventRoutingLockReceipt(ctx, tx, claim.OutboxID)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	receipt, err := routingReceiptFromSQL(row)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	if _, err := routingRecipient(receipt, claim); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	attempts := receipt.RecipientProgress[claim.SubscriptionID].Attempts + 1
	if receipt.RecipientClaims {
		r, err := q.EventRoutingLockRecipient(ctx, tx, sqlc.EventRoutingLockRecipientParams{OutboxID: claim.OutboxID, SubscriptionID: claim.SubscriptionID})
		if err != nil {
			return PublishedEventRoutingResult{}, err
		}
		if r.Generation != claim.Generation {
			return PublishedEventRoutingResult{}, ErrConflict
		}
		attempts = int(r.TotalAttempts)
	}
	if p.matched {
		_, err := q.EventRoutingLockApp(ctx, tx, sqlc.EventRoutingLockAppParams{AppID: mustPgUUID(p.recipient.AppID), AccountID: mustPgUUID(p.recipient.AccountID)})
		if errors.Is(err, pgx.ErrNoRows) {
			return eventAdmissionResult(p, PublishedEventRecipientProgress{}, false, false), admissionError(EventFanoutFailureCodeTargetUnavailable, false, ErrNotFound)
		}
		if err != nil {
			return PublishedEventRoutingResult{}, admissionError(EventFanoutFailureCodeTargetLookupFailed, true, err)
		}
	}
	previous := receipt.RecipientProgress[claim.SubscriptionID]
	if routingAdmissionRecorded(previous) {
		return eventAdmissionResult(p, previous, false, receipt.Delivered), nil
	}
	if err := validateEventRoutingClaim(ctx, q, tx, claim); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	created, err := performEventAdmissionTx(ctx, tx, receipt, p)
	if err != nil {
		return eventAdmissionResult(p, PublishedEventRecipientProgress{}, false, false), admissionError(EventFanoutFailureCodeInvocationEnqueueFailed, true, err)
	}
	progress := eventAdmissionProgress(p, attempts)
	if err := recordEventRecipientOutcome(ctx, q, tx, claim.OutboxID, p.recipient.AppID, claim.SubscriptionID, EventFanoutAttemptActionAttempt, progress); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	// History writes and target updates can themselves wait. Recheck the lease
	// after them, before releasing its ownership; any expiry rolls everything back.
	if err := validateEventRoutingClaim(ctx, q, tx, claim); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	settled, err := settleEventAdmissionTx(ctx, q, tx, claim, receipt.RecipientClaims, progress)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return PublishedEventRoutingResult{}, err
	}
	return eventAdmissionResult(p, progress, created, settled), nil
}

func validateEventRoutingClaim(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, claim PublishedEventRoutingClaim) error {
	valid, err := q.EventRoutingClaimValid(ctx, tx, sqlc.EventRoutingClaimValidParams{ID: claim.OutboxID, SubscriptionID: claim.SubscriptionID, Generation: claim.Generation, ClaimToken: mustPgUUID(claim.ClaimToken)})
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	return nil
}

func performEventAdmissionTx(ctx context.Context, tx pgx.Tx, receipt *PublishedEventWork, p eventAdmissionPlan) (bool, error) {
	if !p.matched || p.prior {
		return false, nil
	}
	if p.cancel {
		cancellation, err := cancelPendingKeyedInvocationsTx(ctx, tx, p.invocation.AppID, p.invocation.WorkPolicyName, p.invocation.WorkKeyDigest, p.invocation.ID)
		if err == nil && cancellation.CreatedAt.Before(receipt.CreatedAt) {
			return false, ErrConflict
		}
		return false, err
	}
	if p.invocation.WorkPolicyName != "" {
		inv, created, err := enqueueKeyedInvocationTx(ctx, tx, p.invocation, p.policy)
		if err == nil && !created && !priorEventInvocationMatches(receipt, p, inv) {
			return false, ErrConflict
		}
		return created, err
	}
	_, err := enqueueInvocationRow(ctx, tx, p.invocation)
	return err == nil, err
}

func priorEventInvocationMatches(receipt *PublishedEventWork, p eventAdmissionPlan, inv Invocation) bool {
	return sameMemUUID(inv.AppID, p.invocation.AppID) && sameMemUUID(inv.AccountID, p.invocation.AccountID) &&
		inv.Source == InvocationAsyncInvoke && !inv.CreatedAt.Before(receipt.CreatedAt) && jsonEqual(inv.Payload, p.invocation.Payload)
}

func settleEventAdmissionTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, claim PublishedEventRoutingClaim, adopted bool, progress PublishedEventRecipientProgress) (bool, error) {
	if adopted {
		n, err := q.EventRecipientFinish(ctx, tx, sqlc.EventRecipientFinishParams{OutboxID: claim.OutboxID, SubscriptionID: claim.SubscriptionID,
			ClaimToken: mustPgUUID(claim.ClaimToken), Generation: claim.Generation, State: progress.State, AvailableAt: pgtypeFromTime(progress.UpdatedAt)})
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, ErrConflict
		}
		if err := q.EventRecipientSettleReceipt(ctx, tx, claim.OutboxID); err != nil {
			return false, err
		}
		row, err := q.EventRoutingReceipt(ctx, tx, claim.OutboxID)
		return row.State == "delivered", err
	}
	_, err := q.EventRoutingSettleSnapshot(ctx, tx, claim.OutboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}
