package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) AdmitEventWorkflowRecipient(ctx context.Context, claim PublishedEventRoutingClaim) (result EventWorkflowRoutingResult, err error) {
	defer func() {
		if errors.Is(err, ErrWorkflowEventDefinitionInvalid) {
			result.Matched = false
		}
		err = workflowRoutingAdmissionError(err)
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	row, err := q.EventRoutingLockReceipt(ctx, tx, claim.OutboxID)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, ErrNotFound
	}
	if err != nil {
		return result, err
	}
	receipt, err := routingReceiptFromSQL(row)
	if err != nil {
		return result, err
	}
	recipient, matched, err := workflowRoutingRecipient(receipt, claim)
	result.Matched = matched
	if err != nil {
		return result, err
	}
	r, err := q.EventRoutingLockRecipient(ctx, tx, sqlc.EventRoutingLockRecipientParams{
		OutboxID: claim.OutboxID, SubscriptionID: claim.SubscriptionID,
	})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && r.Generation != claim.Generation {
		return result, ErrConflict
	}
	if err != nil {
		return result, err
	}
	previous := receipt.RecipientProgress[recipient.ID]
	if routingAdmissionRecorded(previous) {
		prior, err := q.GetEventWorkflowReceipt(ctx, tx, sqlc.GetEventWorkflowReceiptParams{
			OutboxID: receipt.ID, RecipientID: mustPgUUID(recipient.ID),
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return result, err
		}
		result.RunID = uuidString(prior)
		result.Progress, result.ReceiptSettled = previous, receipt.Delivered
		return result, nil
	}
	if err := validateEventRoutingClaim(ctx, q, tx, claim); err != nil {
		return result, err
	}
	var runID string
	var created bool
	if matched {
		runID, created, err = admitEventWorkflowTx(ctx, q, tx, receipt.ID, recipient, receipt.Payload)
		if err != nil {
			return result, err
		}
	}
	progress := workflowRoutingProgress(matched, int(r.TotalAttempts))
	if err := recordEventRecipientOutcome(ctx, q, tx, receipt.ID, recipient.AppID, recipient.ID, EventFanoutAttemptActionAttempt, progress); err != nil {
		return result, err
	}
	// Target/admission and history locks can wait past the lease deadline. Expiry
	// rolls back the run, deduplication receipt, checkpoint and history together.
	if err := validateEventRoutingClaim(ctx, q, tx, claim); err != nil {
		return EventWorkflowRoutingResult{Matched: matched}, err
	}
	settled, err := settleEventAdmissionTx(ctx, q, tx, claim, true, progress)
	if err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	result.RunID, result.RunCreated = runID, created
	result.Progress, result.ReceiptSettled = progress, settled
	return result, nil
}
