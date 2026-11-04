package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// upgradeSubscription upgrades a subject that already has a subscription
// through the provider's API. The customer portal does not switch plans
// (ADR-20261103131742), so this is how an existing subscriber upgrades. It
// reports false when the subject has no subscription with a provider that
// can change it, leaving the caller to start a checkout.
func (s *server) upgradeSubscription(w http.ResponseWriter, r *http.Request, acct state.Account, plan api.Plan) bool {
	billingAcct, changer, ok, err := s.subscriptionChanger(r.Context(), acct)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("billing identity temporarily unavailable"))
		return true
	}
	if !ok {
		return false
	}
	s.scheduleProviderPlanChange(w, r, acct, billingAcct, changer, plan)
	return true
}

// subscriptionChanger returns the subject's provider-qualified billing account
// and the provider surface that changes its subscription; ok is false when the
// subject has no subscription or the provider cannot change one.
func (s *server) subscriptionChanger(ctx context.Context, acct state.Account) (state.Account, billing.SubscriptionPlanChangeProvider, bool, error) {
	if s.billingProvider == nil {
		return state.Account{}, nil, false, nil
	}
	changer, ok := s.billingProvider.(billing.SubscriptionPlanChangeProvider)
	if !ok {
		return state.Account{}, nil, false, nil
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, acct)
	if err != nil {
		return state.Account{}, nil, false, err
	}
	return billingAcct, changer, billingAcct.StripeSubscriptionItem != "", nil
}

// scheduleProviderPlanChange asks the provider to change the subscription and
// answers 202: the local plan follows the provider's webhook, which stays the
// only source of a plan change. An upgrade applies now once its prorated charge
// succeeds; a downgrade applies at the period end.
func (s *server) scheduleProviderPlanChange(w http.ResponseWriter, r *http.Request, acct, billingAcct state.Account, changer billing.SubscriptionPlanChangeProvider, plan api.Plan) {
	upgrade := acct.Plan.RequiresBillingUpgradeTo(plan)
	effectiveAt, err := changer.ChangeSubscriptionPlan(r.Context(), billingAcct, plan)
	if err != nil {
		s.log.Error("schedule plan change failed",
			"account", acct.ID,
			"from", logsanitize.Field(string(acct.Plan)),
			"to", logsanitize.Field(string(plan)),
			"err", logsanitize.FieldAny(err))
		switch {
		case errors.Is(err, billing.ErrAlreadyCancelled):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict,
				api.CodeConflict, "billing subscription unavailable",
				"the billing subscription is no longer active; refresh billing and try again"))
		case errors.Is(err, billing.ErrSubscriptionEnding):
			// A cancelled subscription changes no plan until it is kept
			// (ADR-20261103131742 amendment 1).
			api.WriteProblem(w, s.endingSubscriptionProblem(r.Context(), acct, billingAcct))
		case errors.Is(err, billing.ErrPaymentAuthenticationRequired):
			prob := api.ErrPaymentAuthenticationRequired(plan)
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), billingAcct)
			api.WriteProblem(w, prob)
		case upgrade && errors.Is(err, billing.ErrPaymentDeclined):
			prob := &api.Problem{
				Status: http.StatusPaymentRequired, Code: api.CodePayment, Title: "Upgrade not charged",
				Detail: "the card on file was declined for the upgrade to " + string(plan) + "; nothing was charged and the current plan remains active; update the payment method in the billing portal and try again",
			}
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), billingAcct)
			api.WriteProblem(w, prob)
		case upgrade:
			// Polar applies an upgrade only when its prorated charge succeeds.
			prob := &api.Problem{
				Status: http.StatusPaymentRequired, Code: api.CodePayment, Title: "Upgrade not charged",
				Detail: "the billing provider could not charge the upgrade to " + string(plan) + "; update the payment method in the billing portal and try again; the current plan remains active",
			}
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), billingAcct)
			api.WriteProblem(w, prob)
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_plan_change_failed", "billing plan change failed",
				"the provider could not schedule this plan change; the current plan remains active"))
		}
		return
	}
	updated, err := state.BillingResourceAccount(r.Context(), s.store, acct.ID)
	if err != nil {
		updated = acct
	}
	response := s.accountResponse(r.Context(), updated, r)
	response.PlanChangeStatus = "pending_provider_confirmation"
	response.RequestedPlan = string(plan)
	if !effectiveAt.IsZero() {
		response.EffectiveAt = &effectiveAt
	}
	writeJSON(w, http.StatusAccepted, orgbilling.PlanResponse(updated, response))
}

// createProviderCustomer creates acct's billing customer. A provider that
// keeps one customer per email reports the customer holding acct's email;
// when that customer belongs to a Gregale subject that no longer exists, it
// is a deleted account's or organization's leftover, so it is deleted and
// acct's customer created in its place (ADR-20261103131742). Any other holder
// is a live subject paying with the same email: the caller answers 409.
func (s *server) createProviderCustomer(ctx context.Context, acct state.Account) (string, error) {
	custID, err := s.billingProvider.CreateCustomer(ctx, acct)
	var inUse *billing.CustomerEmailInUseError
	if !errors.As(err, &inUse) || !s.leftoverCustomer(ctx, acct, inUse) {
		return custID, err
	}
	if err := s.billingProvider.(billing.CustomerDeleter).DeleteCustomer(ctx, inUse.CustomerID); err != nil {
		return "", fmt.Errorf("delete a deleted subject's billing customer: %w", err)
	}
	s.audit.Emit(ctx, "billing.leftover_customer_deleted", &acct.ID, map[string]any{
		"provider":    providerName(s.billingProvider),
		"customer_id": inUse.CustomerID,
		"subject_id":  inUse.ExternalID,
	})
	return s.billingProvider.CreateCustomer(ctx, acct)
}

// leftoverCustomer reports whether the customer holding acct's email belongs
// to no live Gregale subject: its subject is gone and no billing identity
// points at it. A customer without a Gregale subject ID is never deleted.
func (s *server) leftoverCustomer(ctx context.Context, acct state.Account, inUse *billing.CustomerEmailInUseError) bool {
	if _, ok := s.billingProvider.(billing.CustomerDeleter); !ok {
		return false
	}
	if inUse.CustomerID == "" || inUse.ExternalID == "" || inUse.ExternalID == acct.ID {
		return false
	}
	if _, err := state.BillingResourceAccount(ctx, s.store, inUse.ExternalID); !errors.Is(err, state.ErrNotFound) {
		return false
	}
	_, err := s.store.AccountByBillingCustomerID(ctx, providerName(s.billingProvider), inUse.CustomerID)
	return errors.Is(err, state.ErrNotFound)
}
