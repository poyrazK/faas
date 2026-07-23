package stripe_test

import (
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestVerifyWebhook_HappyPath(t *testing.T) {
	t.Parallel()

	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	payload := []byte(`{
		"type": "customer.subscription.created",
		"data": {"object": {
			"customer": "cus_test_123",
			"subscription": "sub_test_456",
			"plan": "plan_pro_monthly"
		}}
	}`)
	headers := map[string]string{
		"Stripe-Signature": stripe.SignForTest(payload, testSecret, time.Now()),
	}
	ev, err := c.VerifyWebhook(payload, headers, 5*time.Minute)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	if ev.Type != billing.EventSubscriptionCreated {
		t.Errorf("Type = %v, want EventSubscriptionCreated", ev.Type)
	}
	if ev.CustomerID != "cus_test_123" {
		t.Errorf("CustomerID = %q, want cus_test_123", ev.CustomerID)
	}
	if ev.SubscriptionID != "sub_test_456" {
		t.Errorf("SubscriptionID = %q, want sub_test_456", ev.SubscriptionID)
	}
	if ev.PlanID != "plan_pro_monthly" {
		t.Errorf("PlanID = %q, want plan_pro_monthly", ev.PlanID)
	}
}

func TestVerifyWebhook_MapsAllSubscriptionTypes(t *testing.T) {
	t.Parallel()

	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	cases := []struct {
		name       string
		stripeType string
		wantEvent  billing.EventType
		wantName   string
	}{
		{"subscription_created", "customer.subscription.created", billing.EventSubscriptionCreated, "subscription_created"},
		{"subscription_updated", "customer.subscription.updated", billing.EventSubscriptionUpdated, "subscription_updated"},
		{"subscription_canceled", "customer.subscription.deleted", billing.EventSubscriptionCanceled, "subscription_canceled"},
		{"subscription_past_due", "customer.subscription.past_due", billing.EventSubscriptionPastDue, "subscription_past_due"},
		{"payment_succeeded", "invoice.payment_succeeded", billing.EventPaymentSucceeded, "payment_succeeded"},
		{"payment_failed", "invoice.payment_failed", billing.EventPaymentFailed, "payment_failed"},
		{"unknown_falls_through", "customer.created", billing.EventUnknown, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payload := []byte(`{"type":"` + tc.stripeType + `","data":{"object":{"customer":"cus_unknown"}}}`)
			headers := map[string]string{
				"Stripe-Signature": stripe.SignForTest(payload, testSecret, time.Now()),
			}
			ev, err := c.VerifyWebhook(payload, headers, 5*time.Minute)
			if err != nil {
				t.Fatalf("VerifyWebhook: %v", err)
			}
			if ev.Type != tc.wantEvent {
				t.Errorf("Type = %v, want %v", ev.Type, tc.wantEvent)
			}
			if got := ev.Type.Name(); got != tc.wantName {
				t.Errorf("Type.Name() = %q, want %q", got, tc.wantName)
			}
		})
	}
}

func TestVerifyWebhook_TamperedAfterSign(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	signed := []byte(`{"type":"invoice.payment_succeeded","data":{"object":{"customer":"cus_a"}}}`)
	tampered := []byte(`{"type":"invoice.payment_failed","data":{"object":{"customer":"cus_a"}}}`)
	header := stripe.SignForTest(signed, testSecret, time.Now())

	_, err := c.VerifyWebhook(tampered, map[string]string{"Stripe-Signature": header}, 5*time.Minute)
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyWebhook_MissingSignatureHeader(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	payload := []byte(`{"type":"invoice.payment_succeeded"}`)
	_, err := c.VerifyWebhook(payload, map[string]string{}, 5*time.Minute)
	if !errors.Is(err, billing.ErrBadSignature) {
		t.Fatalf("err = %v, want ErrBadSignature", err)
	}
}

func TestVerifyWebhook_RawIsDefensiveCopy(t *testing.T) {
	t.Parallel()
	store := state.NewMemStore()
	c := stripe.NewClient(store, store, "sk_test_dummy", testSecret, discardLog())

	payload := []byte(`{"type":"customer.subscription.created","data":{"object":{"customer":"cus_copy"}}}`)
	headers := map[string]string{
		"Stripe-Signature": stripe.SignForTest(payload, testSecret, time.Now()),
	}
	ev, err := c.VerifyWebhook(payload, headers, 5*time.Minute)
	if err != nil {
		t.Fatalf("VerifyWebhook: %v", err)
	}
	want := string(payload)
	payload[0] = 'X'
	if got := string(ev.Raw); got != want {
		t.Errorf("Event.Raw mutated by caller-side change: got %q, want %q", got, want)
	}
}
