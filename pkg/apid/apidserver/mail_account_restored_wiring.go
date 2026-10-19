package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) accountRestoredRequested() bool {
	return s.accountRestoredOutboxRequested != nil && s.accountRestoredOutboxRequested()
}

// The verified ingress selects this before its expiring replay claim. Passing
// selection explicitly prevents a late flag withdrawal from sending inline.
func (s *server) handleAccountRestoredOutbox(w http.ResponseWriter, r *http.Request, provider string, ev billing.Event, acct state.Account) bool {
	if ev.Type != billing.EventPaymentSucceeded || !s.accountRestoredRequested() {
		return false
	}
	if err := s.handleSuccessfulPayment(r.Context(), ev, acct, provider == "polar", provider, true); err != nil {
		s.log.Error("account_restored.outbox_transaction_failed", "account_id", acct.ID, "provider", provider)
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return true
	}
	if provider == "polar" {
		s.startPolarInvoicePDF(r.Context(), ev)
	}
	w.WriteHeader(http.StatusOK)
	return true
}

// The credit, subscription, plan, quota and lifecycle operations retain their
// existing ordering. The domain owns only the paired recovery/notification.
func (s *server) handleSuccessfulPayment(ctx context.Context, ev billing.Event, acct state.Account, asyncMail bool, provider string, confirmation bool) error {
	if confirmation && ev.EventID == "" {
		return internalmail.ErrInvalid
	}
	if err := s.applyInvoiceCredits(ctx, providerName(s.billingProvider), acct, ev.Invoice); err != nil {
		return fmt.Errorf("apply invoice credits: %w", err)
	}
	if ev.SubscriptionID != "" {
		if err := s.stampActiveBillingSubscription(ctx, acct, ev.SubscriptionID); err != nil {
			return fmt.Errorf("store payment subscription id: %w", err)
		}
	}
	if plan := billingPlanFromProviderID(ev.PlanID); plan != "" {
		if err := s.setAccountPlan(ctx, acct, plan); err != nil {
			return fmt.Errorf("store payment plan: %w", err)
		}
	}
	restored, outboxed, err := s.restorePaymentWithMail(ctx, ev, acct, provider, confirmation)
	if err != nil {
		return fmt.Errorf("restore account: %w", err)
	}
	if restored {
		s.notifyAccountLifecycle(ctx, acct.ID, "account_reactivated")
		if !outboxed {
			subject, body := mail.AccountRestoredBody(acct.Email, time.Now().UTC())
			s.sendBillingTransitionMail(ctx, acct, subject, body, asyncMail, "payment_succeeded")
		}
	}
	if err := s.store.ClearQuotaWarning(ctx, acct.ID); err != nil {
		return fmt.Errorf("clear quota warning: %w", err)
	}
	return nil
}

func (s *server) restorePaymentWithMail(ctx context.Context, ev billing.Event, acct state.Account, provider string, confirmation bool) (bool, bool, error) {
	if !confirmation {
		restored, err := s.restoreBillingPayment(ctx, ev, acct)
		return restored, false, err
	}
	if s.deletionMailOutbox == nil {
		return false, true, internalmail.ErrDisabled
	}
	if provider == "" && s.billingProvider == nil {
		// Stripe's signed webhook supports operation without its SDK client.
		provider = "stripe"
	}
	restored, err := s.deletionMailOutbox.RestorePaymentWithConfirmation(ctx, acct.ID, provider, ev.EventID, ev.CustomerID)
	return restored, true, err
}
