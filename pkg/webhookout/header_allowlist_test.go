package webhookout

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// Every header a signed outbound webhook carries must cross the gateway's
// guest boundary, or a receiver hosted on Gregale cannot verify it
// (production-us hunt #7, H5-64). This pins api's allowlist to the names the
// dispatcher actually sends.
func TestOutboundWebhookHeadersReachGuests(t *testing.T) {
	for _, set := range []HeaderSet{HeaderSetAlert, HeaderSetWebhook} {
		sig, id, ts, attempt := set.headerNames()
		for _, name := range []string{sig, id, ts, attempt} {
			if !api.IsOutboundWebhookHeader(name) {
				t.Errorf("header set %d sends %s, which the gateway strips before the guest", set, name)
			}
		}
	}
}
