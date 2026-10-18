package mailoutbox

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// RestorePaymentWithConfirmation records each verified provider delivery once.
// A real dunning recovery, its personal-org mirror, pending failure cancellation
// and encrypted confirmation commit together. Replays cannot restore a later
// overdue period, and distinct success events on an active account do not mail.
func (s *Service) RestorePaymentWithConfirmation(ctx context.Context, id, provider, eventID, customerID string) (bool, error) {
	if !s.accountRestoredEnabled() {
		return false, internalmail.ErrDisabled
	}
	if !validID(id) || !validBillingDelivery(provider, eventID, customerID) {
		return false, internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return false, err
	}
	a, err := s.queries.LockAccountRestoredAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrAccountUnavailable
	}
	if err != nil {
		return false, err
	}
	if _, err := s.queries.SharePaymentFailedIdentity(ctx, tx, q.SharePaymentFailedIdentityParams{AccountID: a.ID, Provider: provider, CustomerID: customerID}); err != nil {
		return false, ErrAccountUnavailable
	}
	n, err := s.queries.RecordPaymentRecoveryReceipt(ctx, tx, q.RecordPaymentRecoveryReceiptParams{Provider: provider, EventID: eventID, AccountID: a.ID})
	if err != nil {
		return false, err
	}
	restored := false
	if n == 0 {
		owner, err := s.queries.PaymentRecoveryReceipt(ctx, tx, q.PaymentRecoveryReceiptParams{Provider: provider, EventID: eventID})
		if err != nil {
			return false, err
		}
		if owner != a.ID {
			return false, internalmail.ErrConflict
		}
	} else {
		restored, err = s.commitPaymentRecovery(ctx, tx, a)
		if err != nil {
			return false, err
		}
	}
	if !s.accountRestoredEnabled() {
		return false, internalmail.ErrDisabled
	}
	return restored, tx.Commit(ctx)
}

func (s *Service) commitPaymentRecovery(ctx context.Context, tx pgx.Tx, a q.LockAccountRestoredAccountRow) (bool, error) {
	restorable := a.Status == "past_due" || (a.Status == "suspended" || a.Status == "deleted_pending") && a.PastDueAt.Valid
	if !restorable {
		if a.Status == "active" {
			return false, s.queries.CancelPaymentFailedIntents(ctx, tx, a.ID)
		}
		return false, nil
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return false, err
	}
	if recovery.Held {
		return false, internalmail.ErrRecoveryHeld
	}
	r, err := s.queries.RestoreAccountWithConfirmation(ctx, tx, a.ID)
	if err != nil {
		return false, err
	}
	if err := s.queries.RestorePaymentPersonalOrg(ctx, tx, a.ID); err != nil {
		return false, err
	}
	if err := s.queries.CancelPaymentFailedIntents(ctx, tx, a.ID); err != nil {
		return false, err
	}
	if err := s.saveAccountRestored(ctx, tx, a.ID, a.Email, recovery.RecoveryID, r.RestoredAt.Time, r.BillingMailRevision); err != nil {
		return false, err
	}
	return true, nil
}
