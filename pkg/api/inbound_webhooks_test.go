package api

import "testing"

func TestValidStripeWorkflowCallbackMatch(t *testing.T) {
	for _, tc := range []struct {
		eventType string
		objectID  string
		want      bool
	}{
		{"payment_intent.succeeded", "pi_123", true},
		{"checkout.session.completed", "cs_test_123", true},
		{"", "pi_123", false},
		{"PaymentIntent.Succeeded", "pi_123", false},
		{"payment_intent.succeeded", "pi_\x00", false},
		{"payment_intent.succeeded", "pi_123 bad", false},
	} {
		if got := ValidStripeWorkflowCallbackMatch(tc.eventType, tc.objectID); got != tc.want {
			t.Errorf("ValidStripeWorkflowCallbackMatch(%q, %q) = %t, want %t", tc.eventType, tc.objectID, got, tc.want)
		}
	}
}

func TestValidInboundWebhookEventMetadata(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
		typ  string
		want bool
	}{
		{name: "normal", id: "evt_123:abc", typ: "order.paid", want: true},
		{name: "empty id", id: "", typ: "order.paid"},
		{name: "space id", id: "evt 123", typ: "order.paid"},
		{name: "newline id", id: "evt\n123", typ: "order.paid"},
		{name: "uppercase type", id: "evt_123", typ: "Order.Paid"},
		{name: "wildcard type", id: "evt_123", typ: "order.*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidInboundWebhookEventID(tc.id) && ValidInboundWebhookEventType(tc.typ); got != tc.want {
				t.Fatalf("metadata validation = %t, want %t", got, tc.want)
			}
		})
	}
}
