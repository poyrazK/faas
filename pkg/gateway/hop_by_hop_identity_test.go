// adr: 028
package gateway

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The gateway authors the platform identity headers on the hop it forwards.
// A caller that names them in Connection (an internal service call carries
// the guest's own HTTP/1.1 headers) must not be able to strip the stamped
// caller identity before the callee sees it; ordinary Connection options are
// still removed.
func TestStripHopByHopKeepsPlatformIdentityNamedInConnection(t *testing.T) {
	h := http.Header{}
	api.PlatformIdentity{RequestID: "req-1", AppID: "app-1", TenantID: "acct-caller", InstanceID: "i-1", NodeID: "n-1"}.ApplyGuestHeaders(h)
	h.Set("Connection", "X-Faas-Tenant-Id, x-faas-instance, X-Faas-Request-Id,X-Faas-App-Id, X-Custom-Hop")
	h.Set("X-Custom-Hop", "drop me")

	got := stripHopByHop(h)

	for _, name := range []string{api.TenantIDHeader, api.RequestIDHeader, api.AppIDHeader, api.InstanceIDHeader, "X-Faas-Instance"} {
		if got.Get(name) == "" {
			t.Fatalf("%s was stripped via Connection; headers=%v", name, got)
		}
	}
	if got.Get("X-Custom-Hop") != "" || got.Get("Connection") != "" {
		t.Fatalf("hop-by-hop fields survived: %v", got)
	}
}
