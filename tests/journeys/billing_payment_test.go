package journeys

import (
	"bytes"
	"net/url"
	"os"
	"testing"
	"time"
)

// Payment journeys (row 151, billing GA step 5). A plan change bills the
// account, so these never use the suite's main account: each runs on its own
// fixture account, named by a token, and only against a target whose billing
// is live with a provider (the Polar sandbox before public billing). See
// "Payment journeys" in README.md.
const (
	// billingFreeTokenEnv names a Free fixture account for the checkout.
	billingFreeTokenEnv = "GREGALE_E2E_BILLING_FREE_TOKEN"
	// billingPaidTokenEnv names a fixture account with a paid subscription.
	billingPaidTokenEnv = "GREGALE_E2E_BILLING_TOKEN"
	// billingCheckoutWaitEnv is how long the checkout journey waits for a
	// person to pay its checkout (a Go duration such as 15m). Unset, the
	// journey stops before payment.
	billingCheckoutWaitEnv = "GREGALE_E2E_BILLING_CHECKOUT_WAIT"
)

// TestBillingCheckout starts a paid upgrade on a Free fixture account: the
// CLI hands off to the provider's hosted checkout, and nothing changes before
// payment. With GREGALE_E2E_BILLING_CHECKOUT_WAIT set it logs the checkout URL
// and waits for a person to pay with a test card, then checks that the
// provider's webhooks moved the account to Hobby and invoiced the order.
// After a paid run the account is no longer Free; it can serve as the
// subscription journey's fixture, and the next checkout needs a fresh account.
func TestBillingCheckout(t *testing.T) {
	tg := billingAccount(t, billingFreeTokenEnv)
	t.Parallel()
	status := liveBilling(t, tg)
	if str(status, "plan") != "free" {
		t.Fatalf("%s must name a Free account; it is on %s", billingFreeTokenEnv, str(status, "plan"))
	}
	var wait time.Duration
	if raw := os.Getenv(billingCheckoutWaitEnv); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			t.Fatalf("%s %q: want a positive Go duration such as 15m", billingCheckoutWaitEnv, raw)
		}
		wait = d
	}
	s := newStory(t)
	checkoutURL := ""

	s.step("checkout_handoff", func(t *testing.T) {
		res := tg.run(t, 2*time.Minute, "billing", "plan", "hobby")
		p := problemDoc(res)
		if res.exit == 0 || num(p, "status") != 402 || !httpsURL(str(p, "checkout_url")) {
			t.Fatalf("billing plan hobby on a Free account: want a 402 with a hosted checkout URL; exit %d: %s",
				res.exit, tg.redact(res.problem()))
		}
		checkoutURL = str(p, "checkout_url")
		u, _ := url.Parse(checkoutURL)
		t.Logf("checkout hosted by %s", u.Host)
	})
	s.check("nothing_changes_before_payment", func(t *testing.T) {
		after := object(t, tg.ok(t, time.Minute, "billing", "status").stdout)
		if str(after, "plan") != "free" || field(after, "subscription_configured") == true {
			t.Fatalf("starting a checkout changed the account before payment: %s", compact(after))
		}
	})
	s.check("cancel_needs_a_subscription", func(t *testing.T) {
		res := tg.run(t, time.Minute, "billing", "cancel", "--yes")
		if res.exit == 0 || res.problemCode() != "billing_already_cancelled" {
			t.Fatalf("billing cancel on a Free account: want billing_already_cancelled; exit %d: %s", res.exit, res.problem())
		}
	})
	if wait == 0 {
		t.Logf("payment not tested: set %s to pay the checkout with a test card", billingCheckoutWaitEnv)
		return
	}
	s.step("pay", func(t *testing.T) {
		t.Logf("pay within %s with a Stripe test card (3DS: 4000 0027 6000 3184): %s", wait, checkoutURL)
		eventually(t, "the provider moves the account to hobby", wait, 10*time.Second, func() (bool, string) {
			doc, observed := billingStatus(t, tg)
			return str(doc, "plan") == "hobby" && field(doc, "subscription_configured") == true, observed
		})
	})
	s.check("order_invoiced", func(t *testing.T) {
		eventually(t, "the checkout order is invoiced", 5*time.Minute, 10*time.Second, func() (bool, string) {
			res := tg.run(t, time.Minute, "billing", "invoices")
			docs, err := decodeObjects(res.stdout)
			if res.exit != 0 || err != nil || len(docs) != 1 {
				return false, res.problem()
			}
			items, _ := field(docs[0], "items").([]any)
			for _, item := range items {
				if str(item, "status") == "paid" {
					return true, compact(item)
				}
			}
			return false, compact(docs[0])
		})
	})
}

// TestBillingSubscription walks a paid fixture subscription through what a
// customer does after checkout: status, invoices, card and portal, spend
// cap, cancelling and keeping the subscription, and scheduling a downgrade
// and undoing it (ADR-20261103131742 amendment 1). It charges nothing new (no
// upgrade) and leaves the subscription renewing on its plan, with its spend
// cap as it found it.
func TestBillingSubscription(t *testing.T) {
	tg := billingAccount(t, billingPaidTokenEnv)
	t.Parallel()
	status := liveBilling(t, tg)
	plan := str(status, "plan")
	if plan == "" || plan == "free" || field(status, "subscription_configured") != true {
		t.Fatalf("%s must name an account with a paid subscription: %s", billingPaidTokenEnv, compact(status))
	}
	if change := scheduledChange(status); change != "" {
		t.Fatalf("the fixture subscription %s; keep it first with: gregale billing plan %s", change, plan)
	}
	t.Cleanup(func() { keepSubscription(t, tg, plan) })
	s := newStory(t)

	s.check("status", func(t *testing.T) {
		if str(status, "account_status") != "active" || field(status, "customer_configured") != true {
			t.Fatalf("a paid fixture account should be active with a provider customer: %s", compact(status))
		}
	})
	s.check("invoices", func(t *testing.T) {
		page := object(t, tg.ok(t, time.Minute, "billing", "invoices").stdout)
		items, _ := field(page, "items").([]any)
		paid := 0
		for _, item := range items {
			start, errStart := time.Parse(time.RFC3339, str(item, "period_start"))
			end, errEnd := time.Parse(time.RFC3339, str(item, "period_end"))
			// An order's period comes from its lines; an invoice whose
			// period collapsed to one instant consumed no credits
			// (ADR-20261028101500).
			if errStart != nil || errEnd != nil || !start.Before(end) {
				t.Errorf("invoice %s has no billing period: %s", str(item, "id"), compact(item))
			}
			if str(item, "currency") == "" || num(item, "total_cents") < 0 {
				t.Errorf("invoice %s: %s", str(item, "id"), compact(item))
			}
			if str(item, "status") == "paid" {
				paid++
			}
		}
		if paid == 0 {
			t.Fatalf("a paid subscription has no paid invoice: %s", compact(page))
		}
	})
	s.check("payment_method", func(t *testing.T) {
		// Polar keeps the card in its own portal, so the CLI shows no card
		// and gives the portal URL; Stripe and Paddle show the card.
		doc := object(t, tg.ok(t, time.Minute, "billing", "payment-method", "--print").stdout)
		if field(doc, "payment_method") == nil {
			if !httpsURL(str(doc, "portal_url")) {
				t.Fatalf("no card on file and no portal to add one: %s", compact(doc))
			}
			return
		}
		if len(str(doc, "payment_method.last4")) != 4 {
			t.Fatalf("payment method: %s", compact(doc))
		}
	})
	s.check("portal", func(t *testing.T) {
		doc := object(t, tg.ok(t, time.Minute, "billing", "portal", "--print").stdout)
		if !httpsURL(str(doc, "url")) {
			t.Fatalf("billing portal: %s", compact(doc))
		}
	})
	s.check("spend_cap", func(t *testing.T) {
		was := field(object(t, tg.ok(t, time.Minute, "billing", "spend-cap").stdout), "overage_cap_cents")
		t.Cleanup(func() { restoreSpendCap(t, tg, was) })
		tg.ok(t, time.Minute, "billing", "spend-cap", "1234")
		if got := str(object(t, tg.ok(t, time.Minute, "billing", "spend-cap").stdout), "overage_cap_cents"); got != "1234" {
			t.Fatalf("spend cap set to 1234 cents reads back as %q", got)
		}
	})
	s.step("cancel", func(t *testing.T) {
		doc := object(t, tg.ok(t, 2*time.Minute, "billing", "cancel", "--yes").stdout)
		ends, err := time.Parse(time.RFC3339, str(doc, "effective_at"))
		if field(doc, "cancel_scheduled") != true || err != nil || !ends.After(time.Now()) {
			t.Fatalf("billing cancel: %s", compact(doc))
		}
		after := object(t, tg.ok(t, time.Minute, "billing", "status").stdout)
		shown, err := time.Parse(time.RFC3339, str(after, "subscription_ends_at"))
		// The plan stays until the period ends, and status shows the day
		// the cancel command promised.
		if str(after, "plan") != plan || err != nil || shown.UTC().Format(time.DateOnly) != ends.UTC().Format(time.DateOnly) {
			t.Fatalf("status after cancelling (cancel said it ends %s): %s", ends.Format(time.RFC3339), compact(after))
		}
	})
	s.step("keep_after_cancel", func(t *testing.T) {
		keepAndCheck(t, tg, plan)
	})
	if lower := lowerPaidPlan(plan); lower != "" {
		s.step("schedule_downgrade", func(t *testing.T) {
			doc := object(t, tg.ok(t, 2*time.Minute, "billing", "plan", lower, "--yes").stdout)
			if str(doc, "plan") != plan || str(doc, "requested_plan") != lower ||
				str(doc, "plan_change_status") != "pending_provider_confirmation" || str(doc, "effective_at") == "" {
				t.Fatalf("a downgrade should be scheduled for the period end: %s", compact(doc))
			}
			after := object(t, tg.ok(t, time.Minute, "billing", "status").stdout)
			if str(after, "plan") != plan || str(after, "scheduled_plan") != lower || str(after, "scheduled_plan_at") == "" {
				t.Fatalf("status after scheduling a downgrade to %s: %s", lower, compact(after))
			}
		})
		s.step("undo_downgrade", func(t *testing.T) {
			keepAndCheck(t, tg, plan)
		})
	}
}

// billingAccount returns a target for the fixture account whose token is in
// env, or skips the journey.
func billingAccount(t *testing.T, env string) *target {
	t.Helper()
	base := needs(t, tierFull, false)
	token := os.Getenv(env)
	if token == "" {
		t.Skipf("needs %s: a dedicated fixture account on a target with live billing (see tests/journeys/README.md)", env)
	}
	return &target{api: base.api, appsDomain: base.appsDomain, token: token, cli: base.cli,
		runID: base.runID, tier: base.tier, home: t.TempDir(), deploySlots: make(chan struct{}, 1)}
}

// liveBilling reads the fixture's billing status and skips the journey when
// the target's billing is off: plan changes there never reach a provider.
func liveBilling(t *testing.T, tg *target) map[string]any {
	t.Helper()
	status := object(t, tg.ok(t, time.Minute, "billing", "status").stdout)
	if field(status, "enabled") != true || str(status, "provider") == "" {
		t.Skipf("billing is not live on this target: %s", compact(status))
	}
	return status
}

// billingStatus reads the status without failing, for polling.
func billingStatus(t *testing.T, tg *target) (map[string]any, string) {
	t.Helper()
	res := tg.run(t, time.Minute, "billing", "status")
	docs, err := decodeObjects(res.stdout)
	if res.exit != 0 || err != nil || len(docs) != 1 {
		return nil, res.problem()
	}
	return docs[0], compact(docs[0])
}

// scheduledChange describes the subscription's scheduled cancellation or
// plan change; empty when none is scheduled.
func scheduledChange(status map[string]any) string {
	switch {
	case str(status, "subscription_ends_at") != "":
		return "is cancelled and ends " + str(status, "subscription_ends_at")
	case str(status, "scheduled_plan") != "":
		return "moves to " + str(status, "scheduled_plan") + " at " + str(status, "scheduled_plan_at")
	}
	return ""
}

// keepAndCheck asks for the current plan, which keeps a subscription whose
// cancellation or downgrade is scheduled, and checks nothing is scheduled
// any more.
func keepAndCheck(t *testing.T, tg *target, plan string) {
	t.Helper()
	doc := object(t, tg.ok(t, 2*time.Minute, "billing", "plan", plan, "--yes").stdout)
	if str(doc, "plan") != plan || str(doc, "plan_change_status") != "scheduled_change_undone" {
		t.Fatalf("billing plan %s should keep the subscription: %s", plan, compact(doc))
	}
	after := object(t, tg.ok(t, time.Minute, "billing", "status").stdout)
	if change := scheduledChange(after); str(after, "plan") != plan || change != "" {
		t.Fatalf("after keeping the subscription it still %s: %s", change, compact(after))
	}
}

// keepSubscription leaves the fixture renewing on plan after a journey that
// stopped between cancelling (or downgrading) and keeping it.
func keepSubscription(t *testing.T, tg *target, plan string) {
	doc, observed := billingStatus(t, tg)
	if doc == nil {
		t.Errorf("cleanup: could not read billing status (%s); check it with: gregale billing status", observed)
		return
	}
	if scheduledChange(doc) == "" {
		return
	}
	if res := tg.run(t, 2*time.Minute, "billing", "plan", plan, "--yes"); res.exit != 0 {
		t.Errorf("cleanup: could not keep the %s subscription (%s); run: gregale billing plan %s", plan, res.problem(), plan)
	}
}

// restoreSpendCap puts back the cap read before the journey changed it. An
// unset cap reads as the plan's default, so clearing restores a cap equal to
// the default and an explicit cap is set again.
func restoreSpendCap(t *testing.T, tg *target, was any) {
	tg.run(t, time.Minute, "billing", "spend-cap", "--clear")
	if was == nil {
		return
	}
	want := str(map[string]any{"v": was}, "v")
	res := tg.run(t, time.Minute, "billing", "spend-cap")
	docs, err := decodeObjects(res.stdout)
	if res.exit == 0 && err == nil && len(docs) == 1 && str(docs[0], "overage_cap_cents") == want {
		return
	}
	if res := tg.run(t, time.Minute, "billing", "spend-cap", want); res.exit != 0 {
		t.Errorf("cleanup: could not restore the spend cap of %s cents (%s)", want, res.problem())
	}
}

// lowerPaidPlan is the paid plan below plan; empty for Hobby, whose only
// downgrade is cancelling.
func lowerPaidPlan(plan string) string {
	return map[string]string{"pro": "hobby", "scale": "pro"}[plan]
}

// problemDoc decodes the RFC 7807 body the CLI prints to stderr in JSON mode.
func problemDoc(r cliResult) map[string]any {
	docs, err := decodeObjects(bytes.TrimSpace(r.stderr))
	if err != nil || len(docs) != 1 {
		return nil
	}
	return docs[0]
}

func httpsURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != ""
}
