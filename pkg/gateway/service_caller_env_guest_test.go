// adr: 20261107090000
package gateway

import (
	"context"
	"testing"
)

// The preview markers ServiceProxy sets cross the guest boundary; the same
// names sent by a client or workload do not. They were stripped with every
// other x-faas-* header, so the documented markers never reached the target.
func TestGuestReceivesTrustedPreviewMarkersOnly(t *testing.T) {
	trusted := context.WithValue(context.Background(), trustedServiceCallerEnvContextKey{}, true)
	for _, header := range []string{ServiceCallerEnvHeader, ServiceCallerPreviewOfHeader} {
		if !guestReceivesFaasHeader(trusted, header) {
			t.Errorf("%s set by the service proxy did not reach the guest", header)
		}
		if guestReceivesFaasHeader(context.Background(), header) {
			t.Errorf("a client-sent %s reached the guest", header)
		}
	}
	if guestReceivesFaasHeader(trusted, ServiceProxyCallerAppHeader) {
		t.Errorf("%s crossed the guest boundary; it stays on the node-local hop", ServiceProxyCallerAppHeader)
	}
}
