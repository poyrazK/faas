// adr: 20261101143527 — a refund for an order apid never projected is
// acknowledged, not failed forever.
package apidserver_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/state"
)

// Polar retries a webhook that fails and disables the endpoint after repeated
// failures, so a refund apid cannot match to an invoice must still be
// acknowledged: it is recorded in the audit log, and no refund row is written.
func TestRefundForAnUnknownInvoiceIsAcknowledged(t *testing.T) {
	srv, store, _, acct := newSubscriptionEndFixture(t, api.PlanPro, state.AccountActive, "sub_1")
	ev := billing.Event{Type: billing.EventRefundProcessed, CustomerID: "polar-cust-1",
		ProviderRefundID: "refund_unknown", ChargeID: "order_never_projected", AmountCents: 500, RefundStatus: "succeeded"}
	if err := srv.HandleBillingEventWithOptions(context.Background(), ev, acct, true); err != nil {
		t.Fatalf("refund for an unknown invoice = %v, want acknowledged", err)
	}
	if _, err := store.GetInvoiceByProviderID(context.Background(), acct.ID, "polar", "order_never_projected"); err == nil {
		t.Fatal("an invoice was invented for the unknown order")
	}
}
