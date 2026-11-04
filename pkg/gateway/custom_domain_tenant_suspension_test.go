// adr: 100 — only verified tenant hostnames have routing authority.
package gateway_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestPGBackendUnverifiedSuspensionPreservesCachedCustomDomain(t *testing.T) {
	const host = "victim.example.com"
	r := &tenantBindingRouter{
		fakeRouter: &fakeRouter{byID: map[string]gateway.App{host: {ID: "victim", AccountID: "owner", CustomDomainRoute: true}}},
		bindings:   map[string]gateway.PlatformTenantHostBinding{},
	}
	b := gateway.NewPGBackend(r, gateway.NewFakeScheduler(""), nil)
	for _, suspended := range []bool{false, true} {
		if suspended {
			r.bindings[host] = gateway.PlatformTenantHostBinding{SurfaceID: "foreign", AppID: "attacker", AccountID: "other", TenantID: "unproved", Suspended: true}
		}
		if app, ok := b.Lookup(context.Background(), host); !ok || app.ID != "victim" || app.PlatformTenantID != "" {
			t.Fatalf("suspended=%v: app=%+v found=%v", suspended, app, ok)
		}
	}
}
