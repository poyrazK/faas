package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
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
	if claim.BackfillJobID != "" {
		recipient, err := eventReplayBackfillClaimTarget(ctx, q, s.pool, claim)
		if err != nil {
			return PublishedEventRoutingResult{}, err
		}
		receipt.replayRecipients = map[string]PublishedEventRecipient{claim.SubscriptionID: recipient}
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
	if claim.BackfillJobID != "" {
		if _, err := q.EventReplayBackfillLockJob(ctx, tx, mustPgUUID(claim.BackfillJobID)); err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	if plan.matched && !plan.prior && plan.invocation.WorkPolicyName != "" {
		if _, err := lockWorkAdmissionLane(ctx, tx, plan.invocation.AppID, plan.invocation.WorkPolicyName, plan.invocation.WorkKeyDigest); err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	var limits api.EventDeliveryLimits
	if eventAdmissionNeedsCapacity(plan) {
		// Reuse the held connection: a second pool acquisition can deadlock
		// concurrent admissions when every connection belongs to a transaction.
		account, err := q.AccountByID(ctx, tx, mustPgUUID(plan.recipient.AccountID))
		if err != nil {
			return PublishedEventRoutingResult{}, mapErr(err)
		}
		limits = api.MustLimitsFor(api.Plan(account.Plan)).EventDeliveries
		if err := lockEventCapacity(ctx, tx, plan.recipient.AccountID, limits); err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	return admitEventRecipientTx(ctx, q, tx, claim, plan, limits)
}

func eventReplayBackfillClaimTarget(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, claim PublishedEventRoutingClaim) (PublishedEventRecipient, error) {
	encoded, err := q.EventReplayBackfillClaimTarget(ctx, db, sqlc.EventReplayBackfillClaimTargetParams{
		JobID: mustPgUUID(claim.BackfillJobID), OutboxID: claim.OutboxID, SubscriptionID: mustPgUUID(claim.SubscriptionID),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return PublishedEventRecipient{}, ErrNotFound
	}
	if err != nil {
		return PublishedEventRecipient{}, err
	}
	var recipient PublishedEventRecipient
	if err := json.Unmarshal(encoded, &recipient); err != nil || recipient.ID != claim.SubscriptionID || !recipient.WorkSnapshotCaptured || recipient.Work != nil || recipient.ObjectNotification != nil || len(recipient.Workflow) != 0 {
		return PublishedEventRecipient{}, ErrConflict
	}
	return recipient, nil
}

func admitEventRecipientTx(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, claim PublishedEventRoutingClaim, p eventAdmissionPlan, limits api.EventDeliveryLimits) (PublishedEventRoutingResult, error) {
	row, err := q.EventRoutingLockReceipt(ctx, tx, claim.OutboxID)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	receipt, err := routingReceiptFromSQL(row)
	if err != nil {
		return PublishedEventRoutingResult{}, err
	}
	if claim.BackfillJobID != "" {
		receipt.replayRecipients = map[string]PublishedEventRecipient{claim.SubscriptionID: p.recipient}
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

	var scope string
	if eventAdmissionNeedsCapacity(p) {
		scope, err = eventCapacityTx(ctx, tx, p, limits)
		if err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	var created bool
	controlReason := ""
	var controlNext time.Time
	if scope == "" {
		controlReason, controlNext, err = eventSubscriptionGateTx(ctx, q, tx, p)
		if err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	if scope == "" && controlReason == "" {
		created, err = performEventAdmissionTx(ctx, tx, receipt, p)
	}
	if err != nil {
		return eventAdmissionResult(p, PublishedEventRecipientProgress{}, false, false), admissionError(EventFanoutFailureCodeInvocationEnqueueFailed, true, err)
	}
	progress := eventAdmissionProgress(p, attempts)
	if progress.FilterReason == "schema_version_mismatch" {
		progress = EventSchemaVersionFilteredProgress(previous, progress.Attempts, time.Now().UTC())
	}
	preserveEventCapacityHistory(&progress, previous)
	if scope != "" {
		progress = eventCapacityProgress(previous, attempts, scope)
	}
	if controlReason != "" {
		progress = eventSubscriptionControlProgress(previous, attempts, controlReason, controlNext)
	}
	if created {
		if err := q.EventDeliveryInsertSlot(ctx, tx, sqlc.EventDeliveryInsertSlotParams{InvocationID: mustPgUUID(p.invocation.ID),
			AccountID: mustPgUUID(p.recipient.AccountID), AppID: mustPgUUID(p.recipient.AppID), SubscriptionID: p.recipient.ID}); err != nil {
			return PublishedEventRoutingResult{}, err
		}
	}
	action := EventFanoutAttemptActionAttempt
	if claim.BackfillJobID != "" {
		action = EventFanoutAttemptActionBackfill
	}
	if err := recordEventRecipientOutcome(ctx, q, tx, claim.OutboxID, p.recipient.AppID, claim.SubscriptionID, action, progress); err != nil {
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
	result := eventAdmissionResult(p, progress, created, settled)
	result.CapacityDeferred = scope != ""
	return result, nil
}

func validateEventRoutingClaim(ctx context.Context, q *sqlc.Queries, tx pgx.Tx, claim PublishedEventRoutingClaim) error {
	valid, err := q.EventRoutingClaimValid(ctx, tx, sqlc.EventRoutingClaimValidParams{ID: claim.OutboxID, SubscriptionID: claim.SubscriptionID, Generation: claim.Generation, ClaimToken: mustPgUUID(claim.ClaimToken)})
	if err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	if claim.BackfillJobID != "" {
		valid, err := q.EventReplayBackfillClaimValid(ctx, tx, sqlc.EventReplayBackfillClaimValidParams{
			JobID: mustPgUUID(claim.BackfillJobID), OutboxID: claim.OutboxID,
			SubscriptionID: mustPgUUID(claim.SubscriptionID), Generation: claim.Generation, ClaimToken: mustPgUUID(claim.ClaimToken),
		})
		if err != nil {
			return err
		}
		if !valid {
			return ErrConflict
		}
	}
	return nil
}

func performEventAdmissionTx(ctx context.Context, tx pgx.Tx, receipt *PublishedEventWork, p eventAdmissionPlan) (bool, error) {
	if !p.matched || p.prior {
		return false, nil
	}
	if !p.deliveryDeadline.IsZero() && !p.deliveryDeadline.After(time.Now().UTC()) {
		return false, ErrEventDeliveryExpired
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
			ClaimToken: mustPgUUID(claim.ClaimToken), Generation: claim.Generation, State: progress.State, ControlDeferred: (progress.DeliveryControlReason != "" || progress.State == PublishedEventRecipientFiltered && progress.FilterReason == "schema_version_mismatch"), CapacityDeferred: progress.CapacityScope != "", AvailableAt: pgtypeFromTime(eventProgressNext(progress))})
		if err != nil {
			return false, err
		}
		if n == 0 {
			return false, ErrConflict
		}
		if claim.BackfillJobID != "" {
			updated, err := q.EventReplayBackfillFinishItem(ctx, tx, sqlc.EventReplayBackfillFinishItemParams{
				JobID: mustPgUUID(claim.BackfillJobID), OutboxID: claim.OutboxID, State: progress.State,
				Attempts: int32(progress.Attempts), FailureCode: progress.FailureCode, LastError: progress.LastError, Retryable: progress.Retryable,
			})
			if err != nil {
				return false, err
			}
			if updated == 0 {
				return false, ErrConflict
			}
			if err := q.EventReplayBackfillFinalize(ctx, tx, mustPgUUID(claim.BackfillJobID)); err != nil {
				return false, err
			}
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
