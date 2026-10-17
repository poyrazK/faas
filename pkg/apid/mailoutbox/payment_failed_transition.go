package mailoutbox

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// RecordPaymentFailure commits the first active -> past_due transition, its
// notification and the permanent provider receipt together. The signed ingress
// selects this path before taking a legacy, expiring replay claim. Distinct
// failure/past-due events in one overdue period share the account status guard.
func (s *Service) RecordPaymentFailure(ctx context.Context, id, provider, eventID, customerID string, kind billing.EventType) error {
	if !s.paymentFailedEnabled() {
		return internalmail.ErrDisabled
	}
	if !validID(id) || !validBillingFailure(provider, eventID, customerID, kind) {
		return internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	a, err := s.queries.LockPaymentFailedAccount(ctx, tx, pgID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAccountUnavailable
	}
	if err != nil {
		return err
	}
	if _, err := s.queries.SharePaymentFailedIdentity(ctx, tx, q.SharePaymentFailedIdentityParams{AccountID: a.ID, Provider: provider, CustomerID: customerID}); err != nil {
		return ErrAccountUnavailable
	}
	n, err := s.queries.RecordPaymentFailureReceipt(ctx, tx, q.RecordPaymentFailureReceiptParams{Provider: provider, EventID: eventID, AccountID: a.ID, EventType: kind.Name()})
	if err != nil {
		return err
	}
	if n == 0 {
		r, err := s.queries.PaymentFailureReceipt(ctx, tx, q.PaymentFailureReceiptParams{Provider: provider, EventID: eventID})
		if err != nil {
			return err
		}
		if r.AccountID != a.ID || r.EventType != kind.Name() {
			return internalmail.ErrConflict
		}
	} else if a.Status == "active" {
		if err := s.commitPaymentFailureIntent(ctx, tx, a); err != nil {
			return err
		}
	}
	if !s.paymentFailedEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}

func (s *Service) commitPaymentFailureIntent(ctx context.Context, tx pgx.Tx, a q.LockPaymentFailedAccountRow) error {
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	at, err := s.queries.EnterPaymentPastDue(ctx, tx, a.ID)
	if err != nil {
		return err
	}
	return s.savePaymentFailed(ctx, tx, a.ID, a.Email, recovery.RecoveryID, at.Time)
}

func validBillingDelivery(provider, eventID, customerID string) bool {
	return (provider == "stripe" || provider == "paddle" || provider == "polar") &&
		len(eventID) > 0 && len(eventID) <= api.InternalMailPaymentFailureEventIDMaxBytes && !strings.ContainsAny(eventID, "\r\n\x00") && customerID != ""
}

func validBillingFailure(provider, eventID, customerID string, kind billing.EventType) bool {
	return validBillingDelivery(provider, eventID, customerID) && (kind == billing.EventPaymentFailed || kind == billing.EventSubscriptionPastDue)
}

// RestorePaymentAfterFailure reviews current status under the same account
// lock as failure issuance and dispatch. A webhook's earlier account snapshot
// cannot miss a concurrent failure transition. Recovery cancels pending intent
// even with the worker paused; maild independently fences admitted warnings.
func (s *Service) RestorePaymentAfterFailure(ctx context.Context, id, provider, customerID string) (bool, error) {
	if !s.paymentFailedEnabled() {
		return false, internalmail.ErrDisabled
	}
	if !validID(id) || !validBillingDelivery(provider, "recovery", customerID) {
		return false, internalmail.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer rollback(ctx, tx)
	a, err := s.queries.LockPaymentFailedAccount(ctx, tx, pgID(id))
	if err != nil {
		return false, err
	}
	if _, err := s.queries.SharePaymentFailedIdentity(ctx, tx, q.SharePaymentFailedIdentityParams{AccountID: a.ID, Provider: provider, CustomerID: customerID}); err != nil {
		return false, ErrAccountUnavailable
	}
	n, err := s.queries.RestorePaymentAccount(ctx, tx, a.ID)
	if err != nil {
		return false, err
	}
	if n == 1 {
		if err := s.queries.RestorePaymentPersonalOrg(ctx, tx, a.ID); err != nil {
			return false, err
		}
	}
	if n == 1 || a.Status == "active" {
		if err := s.queries.CancelPaymentFailedIntents(ctx, tx, a.ID); err != nil {
			return false, err
		}
	}
	if !s.paymentFailedEnabled() {
		return false, internalmail.ErrDisabled
	}
	return n == 1, tx.Commit(ctx)
}
