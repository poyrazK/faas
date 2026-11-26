package apidserver_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProductControlOwnerStripeReminder(t *testing.T) {
	t.Run("paid after review", func(t *testing.T) { testProductControlOwnerStripeReminder(t, true) })
	t.Run("retry accepted reminder", func(t *testing.T) { testProductControlOwnerStripeReminder(t, false) })
}

func testProductControlOwnerStripeReminder(t *testing.T, paidAfterReview bool) {
	t.Helper()
	fixture := newStripeOwnerFixture(t)
	_, stripe := fixture.draft(t, "Reminder workspace", "cus_due", "in_due", "customer@example.com", fixture.month)
	dashboard := fixture.dashboard(t, stripe)
	page := dashboard.call(t, "", nil, http.StatusOK, "Review and send")
	form := ownerConsoleForm(t, page, "send_stripe_invoice")
	body := dashboard.call(t, "/review", form, http.StatusOK, "customer@example.com")
	dashboard.call(t, "/confirm", ownerConsoleForm(t, body, "confirm"), http.StatusOK, "Stripe accepted the send for invoice in_due")
	stripe.assertWrites(t, 1, 1, 1, 1)

	due := fixture.now.AddDate(0, 0, 30).Truncate(24 * time.Hour)
	fixture.now = due.Add(time.Hour)
	page = dashboard.call(t, "", nil, http.StatusOK, "Review and send reminder")
	form = ownerConsoleForm(t, page, "send_stripe_reminder")
	body = dashboard.call(t, "/review", form, http.StatusOK, "Review Stripe payment reminder",
		"customer@example.com", "Outstanding balance", "EUR 9.00", due.Format(time.DateOnly), "Test")
	confirm := ownerConsoleForm(t, body, "confirm")

	if paidAfterReview {
		stripe.mu.Lock()
		stripe.status, stripe.amountRemaining = "paid", 0
		stripe.mu.Unlock()
		dashboard.call(t, "/confirm", confirm, http.StatusConflict, "The Stripe invoice is no longer open and overdue.")
		stripe.assertWrites(t, 1, 1, 1, 1)
		export, err := fixture.store.GetStatementInvoiceExport(t.Context(), fixture.acct.ID, state.StatementSourceProductControl, stripe.statement)
		if err != nil || export.PaymentStatus != state.InvoicePaymentPaid {
			t.Fatalf("paid invoice observation = %+v, %v", export, err)
		}
		attempt, err := fixture.store.GetProductStripeInvoiceSend(t.Context(), fixture.acct.ID, stripe.export)
		if err != nil || !attempt.Reminder.StartedAt.IsZero() {
			t.Fatalf("paid invoice reminder attempt = %+v, %v", attempt, err)
		}
		return
	}

	stripe.mu.Lock()
	stripe.amountRemaining = 600
	stripe.loseReminderReply = true
	stripe.mu.Unlock()
	dashboard.call(t, "/confirm", confirm, http.StatusConflict, "balance, or payment terms changed")
	stripe.assertWrites(t, 1, 1, 1, 1)
	body = dashboard.call(t, "/review", form, http.StatusOK, "EUR 6.00")
	confirm = ownerConsoleForm(t, body, "confirm")
	body = dashboard.call(t, "/confirm", confirm, http.StatusBadGateway, "Retry reminder")
	stripe.assertWrites(t, 1, 1, 2, 2)

	started := fixture.now
	dashboard.connectStripe(t, fixture.owner, "rk_test_stripe_rotated")
	page = dashboard.call(t, "", nil, http.StatusOK, "Stripe reminder pending")
	fixture.now = started.Add(api.ProductStripeInvoiceSendRetryWindow)
	page = dashboard.call(t, "", nil, http.StatusOK, "Stripe reminder needs review", "Open invoice in Stripe")
	if strings.Contains(page, "Review and send reminder") {
		t.Fatal("expired reminder remains available for review")
	}
	dashboard.call(t, "/confirm", confirm, http.StatusConflict, "The reminder retry window ended.")
	stripe.assertWrites(t, 1, 1, 2, 2)

	fixture.now = started.Add(time.Second)
	retry := ownerConsoleForm(t, body, "confirm")
	dashboard.call(t, "/confirm", retry, http.StatusOK, "Stripe accepted the reminder for invoice in_due")
	dashboard.call(t, "/confirm", retry, http.StatusOK, "Stripe accepted the reminder for invoice in_due")
	stripe.assertWrites(t, 1, 1, 3, 2)
	stripe.mu.Lock()
	initialKey, reminderKey, retryKey := stripe.sendKeys[0], stripe.sendKeys[1], stripe.sendKeys[2]
	stripe.mu.Unlock()
	if initialKey == reminderKey || reminderKey != retryKey {
		t.Fatalf("reminder keys failed to isolate the original send or deduplicate the retry")
	}
	attempt, err := fixture.store.GetProductStripeInvoiceSend(t.Context(), fixture.acct.ID, stripe.export)
	if err != nil || attempt.Stage != state.StripeInvoiceSent || !attempt.Reminder.StartedAt.Equal(started) ||
		!attempt.Reminder.SentAt.Equal(fixture.now) {
		t.Fatalf("reminder send attempt = %+v, %v", attempt, err)
	}
	page = dashboard.call(t, "", nil, http.StatusOK, "Stripe accepted reminder at")
	if strings.Contains(page, "Review and send reminder") {
		t.Fatal("confirmed reminder is still offered")
	}
}
