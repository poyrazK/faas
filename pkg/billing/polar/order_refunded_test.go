package polar

import (
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
)

// Billing review R7-4: order.refunded carries the Order, so its order id is
// data.id and it names no refund; refund.created names both. Only the refund
// delivery records a refund, and both correlate to the order.
func TestVerifyWebhookRefundDeliveriesNameTheOrder(t *testing.T) {
	p, err := NewProvider(testConfig("http://example.test"), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body, wantRefund string
		wantAmount             int64
	}{
		{"order.refunded", `{"type":"order.refunded","data":{"id":"order-9","customer_id":"customer-1","status":"refunded","refunded_amount":500,"currency":"usd"}}`, "", 500},
		{"refund.created", `{"type":"refund.created","data":{"id":"refund-9","order_id":"order-9","customer_id":"customer-1","status":"succeeded","amount":500,"currency":"usd"}}`, "refund-9", 500},
		// Polar's refund amount excludes tax; the invoice total includes it.
		// A full refund of a $10.00 order (8.33 + 1.67 tax) records 1000.
		{"refund.created with tax", `{"type":"refund.created","data":{"id":"refund-9","order_id":"order-9","customer_id":"customer-1","status":"succeeded","amount":833,"tax_amount":167,"currency":"usd"}}`, "refund-9", 1000},
		{"refund.updated legacy amount field", `{"type":"refund.updated","data":{"id":"refund-9","order_id":"order-9","customer_id":"customer-1","status":"succeeded","amount_refunded":833,"tax_amount":167,"currency":"usd"}}`, "refund-9", 1000},
		// No base amount: record nothing rather than a tax-only figure.
		{"refund.updated tax only", `{"type":"refund.updated","data":{"id":"refund-9","order_id":"order-9","customer_id":"customer-1","status":"succeeded","tax_amount":167,"currency":"usd"}}`, "refund-9", 0},
		{"order.refunded with tax", `{"type":"order.refunded","data":{"id":"order-9","customer_id":"customer-1","status":"refunded","refunded_amount":833,"refunded_tax_amount":167,"currency":"usd"}}`, "", 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			when := time.Now().UTC()
			body := []byte(tc.body)
			ev, err := p.VerifyWebhook(body, map[string]string{
				"webhook-id":        tc.name,
				"webhook-timestamp": strconv.FormatInt(when.Unix(), 10),
				"webhook-signature": SignForTest(body, "webhook-secret", tc.name, when),
			}, 5*time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if ev.Type != billing.EventRefundProcessed || ev.ChargeID != "order-9" || ev.ProviderRefundID != tc.wantRefund {
				t.Fatalf("event = type %v charge %q refund %q, want refund event for order-9 with refund %q", ev.Type, ev.ChargeID, ev.ProviderRefundID, tc.wantRefund)
			}
			if ev.AmountCents != tc.wantAmount {
				t.Fatalf("AmountCents = %d, want %d (tax-inclusive)", ev.AmountCents, tc.wantAmount)
			}
		})
	}
}
