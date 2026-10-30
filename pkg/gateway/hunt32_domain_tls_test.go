package gateway

import (
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// gatewayd-public runs the ask policy on state.EdgeStore. The plan check used
// an optional seam EdgeStore did not implement, so it was skipped in
// production and downgraded wildcards kept getting certificates (bug hunt 32
// M1). The store interface now requires the owner lookups.
var _ OnDemandTLSStore = (*state.EdgeStore)(nil)

// An edge rule's Set-Cookie for the shared apps domain reached every other
// tenant's app: only the guest's own Set-Cookie headers were scoped (bug hunt
// 32 M2).
func TestEdgeRuleResponseSetCookieIsTenantScoped(t *testing.T) {
	d, err := hostidentity.NewDomains("gregale.dev", "gregale.app", "gregale.app", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hostidentity.Configure(d))

	hdr := http.Header{}
	applyResponseHeaderOp(hdr, EdgeRuleHeaderOp{Name: "Set-Cookie", Value: "sid=x; Domain=gregale.app; Path=/", Action: "add"})
	applyResponseHeaderOp(hdr, EdgeRuleHeaderOp{Name: "set-cookie", Value: "pref=1; Domain=shop.example.com", Action: "add"})
	applyResponseHeaderOp(hdr, EdgeRuleHeaderOp{Name: "X-Frame-Options", Value: "DENY; Domain=gregale.app", Action: "set"})
	got := hdr.Values("Set-Cookie")
	if len(got) != 2 || got[0] != "sid=x; Path=/" || got[1] != "pref=1; Domain=shop.example.com" {
		t.Fatalf("Set-Cookie = %q, want the owned Domain dropped and a custom one kept", got)
	}
	if v := hdr.Get("X-Frame-Options"); v != "DENY; Domain=gregale.app" {
		t.Fatalf("a non-cookie header was rewritten: %q", v)
	}
}

// "app.example.com." names the same host as "app.example.com". Keeping the
// root dot missed the exact custom-domain row and host-scoped edge rules
// while the wildcard lookup still matched (bug hunt 32 M3).
func TestHostnameDropsTheRootDot(t *testing.T) {
	for in, want := range map[string]string{
		"App.Example.COM.":     "app.example.com",
		"app.example.com.:443": "app.example.com",
		"app.example.com":      "app.example.com",
		"app.example.com:8080": "app.example.com",
	} {
		if got := hostname(in); got != want {
			t.Errorf("hostname(%q) = %q, want %q", in, got, want)
		}
	}
}
