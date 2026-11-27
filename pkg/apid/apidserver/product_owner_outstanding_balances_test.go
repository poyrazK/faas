package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProductControlOwnerOutstandingBalances(t *testing.T) {
	fixture := newStripeOwnerFixture(t)
	_, stripe := fixture.draft(t, "Balance workspace", "cus_balance", "in_balance", "customer@example.com", fixture.month)
	dashboard := fixture.dashboard(t, stripe)
	page := dashboard.call(t, "", nil, http.StatusOK, "Outstanding balances")
	review := ownerConsoleForm(t, page, "send_stripe_invoice")
	body := dashboard.call(t, "/review", review, http.StatusOK, "EUR 9.00")
	dashboard.call(t, "/confirm", ownerConsoleForm(t, body, "confirm"), http.StatusOK, "Stripe accepted the send for invoice in_balance")

	const summary = "/outstanding-balances"
	dashboard.call(t, summary, nil, http.StatusOK, "No outstanding live invoices.")
	dashboard.call(t, summary+"?mode=test", nil, http.StatusOK, "Current", "EUR 9.00", "Test invoices")
	dashboard.call(t, summary+"?mode=all", nil, http.StatusBadRequest)

	due := fixture.now.AddDate(0, 0, 30).Truncate(24 * time.Hour)
	fixture.now = due.Add(24 * time.Hour)
	const group = summary + "?bucket=overdue&currency=EUR&mode=test"
	page = dashboard.call(t, group, nil, http.StatusOK, "1–30 days overdue", "Balance workspace", "Needs refresh", "Review and send reminder")
	refresh := func(page string) string {
		t.Helper()
		form := ownerConsoleForm(t, page, dashboard.base+"/payments/refresh#outstanding-balances")
		if form.Get("balance_mode") != "test" || form.Get("balance_currency") != "EUR" || form.Get("balance_bucket") != "overdue" {
			t.Fatalf("refresh form for outstanding balance = %v", form)
		}
		return dashboard.call(t, "/payments/refresh", form, http.StatusOK, "Outstanding balances")
	}

	stripe.mu.Lock()
	stripe.amountRemaining = 600
	stripe.mu.Unlock()
	page = refresh(page)
	if !strings.Contains(page, "EUR 6.00") || strings.Contains(page, "EUR 9.00") || strings.Contains(page, "Needs refresh") {
		t.Fatal("refresh did not replace the stale balance with the partial payment")
	}
	reminder := ownerConsoleForm(t, page, "send_stripe_reminder")
	body = dashboard.call(t, "/review", reminder, http.StatusOK, "Review Stripe payment reminder", "EUR 6.00", "Return to outstanding balances")
	dashboard.call(t, "/confirm", ownerConsoleForm(t, body, "confirm"), http.StatusOK,
		"Stripe accepted the reminder for invoice in_balance", "Return to outstanding balances",
		dashboard.base+summary+"?bucket=overdue&amp;currency=EUR&amp;mode=test")
	stripe.assertWrites(t, 1, 1, 2, 2)

	fixture.now = fixture.now.Add(time.Minute)
	page = dashboard.call(t, group, nil, http.StatusOK, "Stripe accepted reminder at")
	stripe.mu.Lock()
	stripe.status, stripe.amountRemaining = "paid", 0
	stripe.mu.Unlock()
	page = refresh(page)
	if strings.Contains(page, "Balance workspace") || strings.Contains(page, "EUR 6.00") ||
		!strings.Contains(page, "Payment confirmed for invoice in_balance") || !strings.Contains(page, "No outstanding test invoices.") {
		t.Fatal("paid invoice remained in the outstanding balances view")
	}
	stripe.assertWrites(t, 1, 1, 2, 2)
}
