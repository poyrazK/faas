package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/orgbilling"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/state"
)

// planChangeScheduledChangeUndone is the plan_change_status of a request for
// the current plan that undid the subscription's scheduled cancellation or
// downgrade (ADR-20261103131742 amendment 1).
const planChangeScheduledChangeUndone = "scheduled_change_undone"

// subscriptionState reads billingAcct's subscription from the provider,
// bounded like the webhook path's read. ok is false when the subject has no
// subscription, the provider cannot report one, or the read failed.
func (s *server) subscriptionState(ctx context.Context, billingAcct state.Account) (billing.SubscriptionState, bool) {
	reader, ok := s.billingProvider.(billing.SubscriptionStateProvider)
	if !ok || billingAcct.StripeSubscriptionItem == "" {
		return billing.SubscriptionState{}, false
	}
	readCtx, cancel := context.WithTimeout(ctx, billingStateTimeout)
	defer cancel()
	current, err := reader.SubscriptionState(readCtx, billingAcct.StripeSubscriptionItem)
	if err != nil {
		s.log.Warn("billing: subscription state unavailable", "account", billingAcct.ID, "err", logsanitize.Field(err.Error()))
		return billing.SubscriptionState{}, false
	}
	return current, true
}

// scheduledChangeFields copies a live subscription's scheduled cancellation
// and scheduled plan change onto the billing status.
func scheduledChangeFields(out *api.BillingStatusResponse, current billing.SubscriptionState) {
	if !current.Status.Live() {
		return
	}
	if current.CancelAtPeriodEnd && !current.EndsAt.IsZero() {
		endsAt := current.EndsAt.UTC()
		out.SubscriptionEndsAt = &endsAt
	}
	if plan := api.Plan(current.PendingPlanID); plan.Valid() && !current.PendingAt.IsZero() {
		at := current.PendingAt.UTC()
		out.ScheduledPlan, out.ScheduledPlanAt = plan, &at
	}
}

// scheduledChangeNotice describes acct's scheduled cancellation or downgrade
// for the dashboard; ending is true when the subscription ends instead of
// moving to another plan. Empty when nothing is scheduled or the provider
// could not be read.
func (s *server) scheduledChangeNotice(ctx context.Context, acct state.Account) (notice string, ending bool) {
	if s.billingProvider == nil || !acct.Plan.IsPaid() {
		return "", false
	}
	billingAcct, err := s.accountForActiveBillingProvider(ctx, acct)
	if err != nil {
		return "", false
	}
	current, ok := s.subscriptionState(ctx, billingAcct)
	if !ok {
		return "", false
	}
	var out api.BillingStatusResponse
	scheduledChangeFields(&out, current)
	switch {
	case out.SubscriptionEndsAt != nil:
		return fmt.Sprintf("Your %s subscription is cancelled: it ends on %s and the account then moves to the Free plan.",
			acct.Plan, out.SubscriptionEndsAt.Format("2006-01-02")), true
	case out.ScheduledPlan != "":
		return fmt.Sprintf("Your subscription moves from %s to %s on %s.",
			acct.Plan, out.ScheduledPlan, out.ScheduledPlanAt.Format("2006-01-02")), false
	}
	return "", false
}

// keepSubscription undoes the scheduled cancellation or downgrade of acct's
// subscription, so it renews on its current plan (ADR-20261103131742
// amendment 1). The provider refuses every plan change while a cancellation is
// scheduled and otherwise allows undoing one only in its customer portal. ok
// is false when acct has no subscription the provider can keep; undone is
// false when nothing was scheduled. A subscription that already ended is
// billing.ErrSubscriptionGone.
func (s *server) keepSubscription(ctx context.Context, acct state.Account) (billingAcct state.Account, undone, ok bool, err error) {
	resumer, canResume := s.billingProvider.(billing.SubscriptionResumer)
	if !canResume || !acct.Plan.IsPaid() {
		return acct, false, false, nil
	}
	billingAcct, err = s.accountForActiveBillingProvider(ctx, acct)
	if err != nil {
		return acct, false, true, fmt.Errorf("load billing identity: %w", err)
	}
	if billingAcct.StripeSubscriptionItem == "" {
		return billingAcct, false, false, nil
	}
	undone, err = resumer.ResumeSubscription(ctx, billingAcct)
	if err != nil {
		return billingAcct, false, true, err
	}
	if undone {
		s.audit.Emit(ctx, "billing.subscription_kept", &acct.ID, map[string]any{
			"plan":            string(acct.Plan),
			"subscription_id": billingAcct.StripeSubscriptionItem,
		})
	}
	return billingAcct, undone, true, nil
}

// keepSubscriptionPlan answers a request for the plan the subject already
// pays for: a subscription scheduled to end or to move to another plan
// renews on its current plan instead. It reports false when there is no
// subscription to keep, and the caller answers as before.
func (s *server) keepSubscriptionPlan(w http.ResponseWriter, r *http.Request, acct state.Account) bool {
	billingAcct, undone, ok, err := s.keepSubscription(r.Context(), acct)
	switch {
	case !ok:
		return false
	case errors.Is(err, billing.ErrSubscriptionGone):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"billing subscription unavailable",
			"the billing subscription has already ended; refresh billing and start a new subscription"))
		return true
	case err != nil:
		s.log.Error("keep subscription failed", "account", acct.ID, "err", logsanitize.FieldAny(err))
		prob := api.NewProblem(http.StatusBadGateway, "billing_plan_change_failed", "billing plan change failed",
			"the provider could not undo the scheduled change, which still applies; try again or keep the subscription in the billing portal")
		if billingAcct.StripeSubscriptionItem != "" {
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), billingAcct)
		}
		api.WriteProblem(w, prob)
		return true
	}
	updated, err := state.BillingResourceAccount(r.Context(), s.store, acct.ID)
	if err != nil {
		updated = acct
	}
	response := s.accountResponse(r.Context(), updated, r)
	if undone {
		response.PlanChangeStatus = planChangeScheduledChangeUndone
		response.RequestedPlan = string(acct.Plan)
	}
	writeJSON(w, http.StatusOK, orgbilling.PlanResponse(updated, response))
	return true
}

// dashboardKeepSubscription keeps acct's subscription from the dashboard and
// returns the ?upgrade= flag for the billing page.
func (s *server) dashboardKeepSubscription(ctx context.Context, acct state.Account) string {
	_, undone, ok, err := s.keepSubscription(ctx, acct)
	switch {
	case !ok:
		return "unavailable"
	case errors.Is(err, billing.ErrSubscriptionGone):
		return "subscription_ended"
	case err != nil:
		s.log.Error("dashboard: keep subscription", "account", acct.ID, "err", logsanitize.FieldAny(err))
		return "keep_failed"
	case !undone:
		return "nothing_scheduled"
	}
	return "kept"
}

// endingSubscriptionProblem is the refusal of a plan change the provider
// declined because the subscription is scheduled to end, dated when the
// subscription can be read.
func (s *server) endingSubscriptionProblem(ctx context.Context, acct, billingAcct state.Account) *api.Problem {
	var endsAt time.Time
	if current, ok := s.subscriptionState(ctx, billingAcct); ok && current.CancelAtPeriodEnd {
		endsAt = current.EndsAt
	}
	return api.ErrBillingSubscriptionEnding(acct.Plan, endsAt, orgBillingSubject(acct))
}

// planChangeFlag is the dashboard ?upgrade= flag for a failed provider plan
// change.
func planChangeFlag(err error, fallback string) string {
	switch {
	case errors.Is(err, billing.ErrSubscriptionEnding):
		return "subscription_ending"
	case errors.Is(err, billing.ErrPaymentAuthenticationRequired):
		return "authentication_required"
	case errors.Is(err, billing.ErrAlreadyCancelled):
		return "subscription_ended"
	}
	return fallback
}
