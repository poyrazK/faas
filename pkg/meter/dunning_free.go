package meter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// moveToFreeForNonpayment is the dunning ladder's only automatic step since
// the billing review of 2026-11-13 (R9-1, owner decision "move to Free, no
// deletion"): an account whose payment stays overdue past the 7-day grace
// moves to the Free plan and becomes active again, as the Terms (§3) say,
// instead of being suspended and deleted.
//
//   - The plan becomes Free, so apps, databases and storage past the Free
//     plan's limits follow the plan-excess rules like any downgrade.
//   - The status becomes active through MarkDunningStep, which keeps
//     past_due_at: it marks the unpaid balance until a paid order or an
//     operator clears it, and makes later payment-failure events no-ops
//     (state.Account.MovedToFreeForNonpayment).
//   - The account is mailed once (the message ID is per past_due_at).
//
// from is the status the account holds now: past_due, or suspended for an
// account the old ladder suspended.
func (d *Dunning) moveToFreeForNonpayment(ctx context.Context, acct state.Account, from state.AccountStatus, now time.Time) error {
	previous := acct.Plan
	if acct.Plan != api.PlanFree {
		if err := d.p.Store.UpdateAccountPlan(ctx, acct.ID, api.PlanFree); err != nil {
			return fmt.Errorf("plan to free: %w", err)
		}
	}
	if err := d.p.Store.MarkDunningStep(ctx, acct.ID, from, state.AccountActive); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil // the status moved under us: a payment or an operator
		}
		return fmt.Errorf("status to active: %w", err)
	}
	notifyAccountAppLifecycle(ctx, d.p.Store, d.p.Notif, acct.ID, "account_plan_changed", d.p.Log)
	payload, _ := json.Marshal(map[string]any{"account_id": acct.ID, "at": now.UTC().Format(time.RFC3339Nano), "kind": "moved_to_free"})
	if err := d.p.Notif.Notify(ctx, db.NotifyBillingPastDue, string(payload)); err != nil {
		d.p.Log.Warn("meter: dunning notify billing_past_due", "account", acct.ID, "err", err)
	}
	pastDueAt := now
	if acct.PastDueAt != nil {
		pastDueAt = *acct.PastDueAt
	}
	// An organization without a billing email is told through its owner
	// (billing review R10-3).
	if to := billingMailTo(ctx, d.p.Store, acct); to != "" {
		subject, body := mail.MovedToFreeForNonPaymentBody(to, string(previous), pastDueAt, now)
		msg := mail.Message{To: []string{to}, Subject: subject, TextBody: body,
			MessageID: fmt.Sprintf("%s:moved_to_free:%s", acct.ID, pastDueAt.UTC().Format(time.RFC3339))}
		if err := d.p.Mailer.Send(ctx, msg); err != nil {
			d.p.Log.Warn("meter: dunning moved-to-free mail", "account", acct.ID, "err", err)
		}
	}
	d.p.Log.Info("meter: dunning moved to free for non-payment", "account", acct.ID, "from_plan", previous, "from_status", from)
	return nil
}

// quotaSuspended reports whether id is held by the Free quota stop rather
// than suspended for non-payment. A store without the reader has no quota
// stop to tell apart.
func quotaSuspended(ctx context.Context, store any, id string) (bool, error) {
	r, ok := store.(interface {
		QuotaSuspended(context.Context, string) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return r.QuotaSuspended(ctx, id)
}

// deferToProvider reports whether the move to Free waits this tick because
// the provider says the account has paid, or could not be asked. A
// subscription in good standing (the account's own when it is listed, else
// any) means a payment webhook was missed or not accepted; apid's billing
// reconciliation restores the account from the same listing, so meterd
// leaves it alone. A provider error fails the tick (FaasMeterdDunningFailing)
// and retries next tick rather than moving a customer who may have paid.
func (d *Dunning) deferToProvider(ctx context.Context, acct state.Account, fail func(state.Account, string, error)) bool {
	if d.p.Subscriptions == nil || acct.ProviderCustomerID == "" {
		return false
	}
	subs, err := d.p.Subscriptions.CustomerSubscriptions(ctx, acct.ProviderCustomerID)
	if err != nil {
		fail(acct, "provider subscriptions", err)
		return true
	}
	if paidAtProvider(subs, acct.StripeSubscriptionItem) {
		d.p.Log.Info("meter: dunning deferred: provider reports the subscription paid; reconciliation restores the account",
			"account", acct.ID)
		return true
	}
	return false
}

// paidAtProvider reports whether bound (the account's subscription), or any
// subscription when bound is not listed, is in good standing.
func paidAtProvider(subs []billing.Subscription, bound string) bool {
	if bound != "" {
		for _, sub := range subs {
			if sub.ID == bound {
				return sub.Status.InGoodStanding()
			}
		}
	}
	for _, sub := range subs {
		if sub.Status.InGoodStanding() {
			return true
		}
	}
	return false
}
