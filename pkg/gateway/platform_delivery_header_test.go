package gateway

import "testing"

// A public request cannot carry the metadata the platform stamps on the work
// it delivers. Verified inbound webhook metadata was missing from the list:
// production-us 2026-11-07, a direct and an async-route request with
// `X-Gregale-Webhook-Provider: stripe` and `X-Gregale-Webhook-Event-Id`
// reached the app unchanged.
func TestPlatformDeliveryHeadersAreDroppedFromPublicRequests(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]bool{
		"X-Gregale-Webhook-Provider":      true,
		"x-gregale-webhook-event-id":      true,
		"X-Gregale-Webhook-Endpoint-Id":   true,
		"X-Gregale-Event-Id":              true,
		"X-Gregale-Event-Subscription-Id": true,
		"X-Gregale-Operation-Id":          true,
		"X-Gregale-Trace-Id":              false,
		"X-Request-Id":                    false,
		"Content-Type":                    false,
	} {
		if got := isPlatformDeliveryHeader(name); got != want {
			t.Errorf("isPlatformDeliveryHeader(%q) = %v, want %v", name, got, want)
		}
	}
}
