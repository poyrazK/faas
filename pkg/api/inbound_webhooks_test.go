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
