package gateway

import (
	"context"
	"testing"
)

// production-us hunt #7 (H5-64): Gregale's signed alert webhook reached an
// app hosted on Gregale without X-Faas-Alert-Signature, because the guest
// boundary drops inbound x-faas-* headers. Signature headers pass now; other
// client-supplied x-faas-* headers are still dropped.
// adr: 076
func TestGuestReceivesOutboundWebhookSignatureHeaders(t *testing.T) {
	ctx := context.Background()
	for name, want := range map[string]bool{
		"X-Faas-Alert-Signature":   true,
		"X-Faas-Alert-Timestamp":   true,
		"X-Faas-Webhook-Signature": true,
		"x-faas-delivery-id":       true,
		"X-Faas-Debug-Override":    false,
		"X-Faas-Invocation-Source": false,
	} {
		if got := guestReceivesFaasHeader(ctx, name); got != want {
			t.Errorf("guestReceivesFaasHeader(%q) = %v, want %v", name, got, want)
		}
	}
}
