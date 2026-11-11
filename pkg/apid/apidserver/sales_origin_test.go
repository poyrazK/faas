package apidserver

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

// ADR 20261111113147: the independent sales frontend is a normal session
// origin, while provider administration keeps its narrower origin policy.
func TestSalesFrontendOriginIsExactAndNotProviderAdmin(t *testing.T) {
	s := &server{domain: "gregale.dev"}
	for _, raw := range []string{"https://gregale.dev", "https://sales.gregale.dev"} {
		u, _ := url.Parse(raw)
		if !s.isWebDashboardOrigin(u) {
			t.Fatalf("trusted frontend rejected: %s", raw)
		}
	}
	for _, raw := range []string{"http://sales.gregale.dev", "https://sales.gregale.dev.evil.test", "https://sales.gregale.dev:444", "https://customer.gregale.dev"} {
		u, _ := url.Parse(raw)
		if s.isWebDashboardOrigin(u) {
			t.Fatalf("untrusted frontend accepted: %s", raw)
		}
	}
	u, _ := url.Parse("https://sales.gregale.dev")
	if s.isTrustedAdminOrigin(u, httptest.NewRequest("POST", "https://api.gregale.dev/v1/provider", nil)) {
		t.Fatal("sales frontend became a provider administration origin")
	}
}
