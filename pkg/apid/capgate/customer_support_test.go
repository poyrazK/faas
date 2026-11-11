package capgate

import (
	"github.com/onebox-faas/faas/pkg/productcap"
	"testing"
)

func TestCustomerSupportIsOwnedPreviewAndStaffRoutesStayPrivate(t *testing.T) {
	catalog, e := productcap.Load()
	if e != nil {
		t.Fatal(e)
	}
	gate := New(catalog, nil)
	for _, path := range []string{"/v1/support/tickets", "/v1/support/tickets/abc/reopen", "/v1/orgs/acme/support/tickets"} {
		owner, ok := gate.Capability(path)
		if !ok || owner != "customer-support" {
			t.Fatalf("unowned support route %s: %s %v", path, owner, ok)
		}
	}
	if gate.ReleasedByDefault("customer-support") {
		t.Fatal("support preview released without rollout rule")
	}
	if owner, ok := gate.Capability("/v1/admin/support/tickets"); ok {
		t.Fatalf("private staff route unexpectedly customer gated: %s", owner)
	}
}
