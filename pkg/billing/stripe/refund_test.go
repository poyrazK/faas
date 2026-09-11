// Issue #279 PR A — Stripe webhook mapping for charge.refunded.
//
// The Refund gRPC seam is exercised end-to-end through the apid
// handler (cmd/apid/handlers_admin_credits_test.go) and the e2e
// suite (cmd/e2e/credit_e2e_test.go). The contract pinned here is
// the webhook → EventRefundProcessed mapping that the apid
// handler dispatches against, since that's the surface that turns
// a Stripe `charge.refunded` event into a `refund.processed` audit
// row.
package stripe_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestVerifyWebhook_ChargeRefunded — a signed charge.refunded webhook
// maps to EventRefundProcessed and carries the integer cents + charge
// ID + currency we need for the audit row.
func TestVerifyWebhook_ChargeRefunded(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	// 5000 cents = €50.00. Stripe sends the amount in EUR's smallest unit.
	payload := []byte(`{
		"type": "charge.refunded",
		"data": {"object": {
			"id": "ch_test_123",
			"customer": "cus_test_alice",
			"amount_refunded": 5000,
			"currency": "eur",
			"refunded": true,
			"refunds": {"data": [{
				"id": "re_test_123",
				"amount": 5000,
				"currency": "eur",
				"status": "succeeded"
			}]}
		}}
	}`)
	headers := map[string]string{
		"Stripe-Signature": stripe.SignForTest(payload, testSecret, time.Now()),
	}
	ev, err := c.VerifyWebhook(payload, headers, 5*time.Minute)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if ev.Type != billing.EventRefundProcessed {
		t.Fatalf("Type = %v, want EventRefundProcessed", ev.Type)
	}
	if got := ev.Type.Name(); got != "refund_processed" {
		t.Errorf("Type.Name() = %q, want refund_processed", got)
	}
	if ev.ChargeID != "ch_test_123" {
		t.Errorf("ChargeID = %q, want ch_test_123", ev.ChargeID)
	}
	if ev.ProviderRefundID != "re_test_123" || ev.RefundStatus != "succeeded" {
		t.Errorf("refund identity/status = %q/%q, want re_test_123/succeeded", ev.ProviderRefundID, ev.RefundStatus)
	}
	if ev.AmountCents != 5000 {
		t.Errorf("AmountCents = %d, want 5000", ev.AmountCents)
	}
	if ev.Currency != "eur" {
		t.Errorf("Currency = %q, want eur", ev.Currency)
	}
}

// TestCentsToStripeMinorUnits pins the outbound conversion at the Refund call
// site. Stripe accepts integer minor units, so EUR/USD cents must remain
// unchanged. A scaling factor here would silently refund the wrong amount.
// Pinned as a pure helper so the test runs without standing up the
// stripe-go SDK or a live sandbox.
func TestCentsToStripeMinorUnits(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		cents      int64
		minorUnits int64
	}{
		{"zero", 0, 0},
		{"one cent", 1, 1},
		{"common goodwill credit", 500, 500},
		{"euro", 5000, 5000},
		{"largest plausible single-call refund", 100_000_00, 100_000_00}, // €100,000.00
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := stripe.CentsToStripeMinorUnitsForTest(tc.cents); got != tc.minorUnits {
				t.Errorf("centsToStripeMinorUnits(%d) = %d, want %d", tc.cents, got, tc.minorUnits)
			}
		})
	}
}
