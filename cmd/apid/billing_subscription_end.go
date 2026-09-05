package main

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// applySubscriptionEnded is the EventSubscriptionCanceled arm of the
// billing state machine: Polar `subscription.revoked` (and
// `subscription.canceled` with a non-active status), Paddle
// `subscription.canceled`, Stripe `customer.subscription.deleted`.
//
// Spec §4.7 / §10: a cancelled subscription downgrades the account to
// Free at period end. The pre-fix behaviour flipped the account to
// `suspended` and left plan + subscription id untouched, which stranded
// every voluntarily cancelled customer: the dunning sweep skips
// suspended rows with no past_due_at, so the row never moved again, and
// the stale subscription id made PATCH /v1/account/plan skip hosted
// checkout, so the customer could not come back either.
//
// Contract now:
//
//   - the subscription binding is cleared so the next upgrade goes
//     through hosted checkout instead of the provider portal;
//   - the plan is set to Free; meterd's quota tick enforces the Free
//     allowance from the next cycle (Free hard-stops at 100 %);
//   - the account status is NOT touched. An active account stays active
//     (voluntary cancellation — the customer simply is a Free customer
//     again). A past_due / suspended account keeps its stamp: the
//     provider revoking after failed dunning does not forgive the
//     unpaid invoice, and pkg/meter.Dunning still owns the
//     past_due → suspended → deleted_pending ladder.
//
// Stale-event guard: a revoke for a subscription that is not the
// account's current one (the customer re-subscribed before the old
// period ended, so the account already points at the new subscription)
// must not downgrade the live subscription. An event without a
// subscription id (Stripe's legacy payload shape) is trusted.
func (s *server) applySubscriptionEnded(ctx context.Context, ev billing.Event, acct state.Account, asyncMail bool) error {
	if ev.SubscriptionID != "" && acct.StripeSubscriptionItem != "" && ev.SubscriptionID != acct.StripeSubscriptionItem {
		s.log.Info("apid: subscription_canceled for a superseded subscription; ignoring",
			"account", acct.ID,
			"event_subscription", logsanitize.Field(ev.SubscriptionID))
		return nil
	}
	if acct.StripeSubscriptionItem != "" {
		if err := s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, ""); err != nil {
			return fmt.Errorf("clear subscription id: %w", err)
		}
	}
	if acct.Plan != api.PlanFree {
		if err := s.store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
			return fmt.Errorf("downgrade plan to free: %w", err)
		}
	}
	s.audit.Emit(ctx, "billing.subscription_ended", &acct.ID, map[string]any{
		"from_plan":       string(acct.Plan),
		"subscription_id": ev.SubscriptionID,
		"status":          string(acct.Status),
	})
	// "All transitions emailed" (spec §4.7). Only the voluntary path
	// mails here: a past_due / suspended account already received the
	// dunning emails for its state, and a Free account with no
	// subscription (provider redelivery) has nothing to be told.
	if acct.Plan != api.PlanFree && acct.Status == state.AccountActive {
		subject, body := mail.SubscriptionEndedBody(acct.Email, string(acct.Plan), time.Now().UTC())
		s.sendBillingTransitionMail(ctx, acct, subject, body, asyncMail, "subscription_canceled")
	}
	return nil
}
