package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

// Called only after signature verification, account binding and invoice
// projection, before the legacy replay claim. Selection never falls back to
// inline mail on missing configuration, a late gate withdrawal or a failed save.
func (s *server) handlePaymentFailedOutbox(w http.ResponseWriter, r *http.Request, provider string, ev billing.Event, acct state.Account) bool {
	if ev.Type != billing.EventPaymentFailed && ev.Type != billing.EventSubscriptionPastDue ||
		s.paymentFailedOutboxRequested == nil || !s.paymentFailedOutboxRequested() {
		return false
	}
	err := internalmail.ErrDisabled
	if s.deletionMailOutbox != nil {
		err = s.deletionMailOutbox.RecordPaymentFailure(r.Context(), acct.ID, provider, ev.EventID, ev.CustomerID, ev.Type)
	}
	if err != nil {
		s.log.Error("payment_failed.outbox_transaction_failed", "account_id", acct.ID, "provider", provider)
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return true
	}
	w.WriteHeader(http.StatusOK)
	return true
}

func (s *server) restoreBillingPayment(ctx context.Context, ev billing.Event, acct state.Account) (bool, error) {
	if s.paymentFailedOutboxRequested != nil && s.paymentFailedOutboxRequested() {
		if s.deletionMailOutbox == nil {
			return false, internalmail.ErrDisabled
		}
		provider := providerName(s.billingProvider)
		if s.billingProvider == nil {
			// Only Stripe's signed ingress permits a missing SDK client.
			provider = "stripe"
		}
		return s.deletionMailOutbox.RestorePaymentAfterFailure(ctx, acct.ID, provider, ev.CustomerID)
	}
	if acct.Status != state.AccountPastDue &&
		(acct.Status != state.AccountSuspended || acct.PastDueAt == nil) &&
		(acct.Status != state.AccountDeletedPending || acct.PastDueAt == nil) {
		return false, nil
	}
	if err := s.store.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
		return false, err
	}
	return true, nil
}
