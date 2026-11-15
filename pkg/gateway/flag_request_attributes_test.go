package gateway

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// ADR 20261114150000 §2: the gateway stamps the client's country and browser
// family for Flags rules, replacing anything the client sent.
func TestFlagRequestAttributesStampedOnGuestIdentity(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(api.ClientCountryHeader, "XX")
	r.Header.Set(api.ClientUAFamilyHeader, "forged")
	ctx := context.WithValue(r.Context(), flagRequestAttributesKey{}, flagRequestAttributes{country: "DE", uaFamily: "firefox"})
	r = r.WithContext(ctx)
	r, identity := stampTargetIdentity(r, App{ID: "app-1", AccountID: "acct-1"}, Target{AppID: "app-1", DeploymentID: "dep-1", InstanceID: "i-1"})
	if identity.ClientCountry != "DE" || identity.ClientUAFamily != "firefox" {
		t.Fatalf("identity = %+v", identity)
	}
	if got := r.Header.Get(api.ClientCountryHeader); got != "DE" {
		t.Fatalf("country header = %q", got)
	}
	if got := r.Header.Get(api.ClientUAFamilyHeader); got != "firefox" {
		t.Fatalf("ua family header = %q", got)
	}
}

func TestFlagRequestAttributesUnknownSendsNoHeader(t *testing.T) {
	h := &Handler{} // no geo database: country is unknown
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set(api.ClientCountryHeader, "XX")
	r = h.withFlagRequestAttributes(r)
	r, _ = stampTargetIdentity(r, App{ID: "app-1"}, Target{AppID: "app-1"})
	if got := r.Header.Values(api.ClientCountryHeader); len(got) != 0 {
		t.Fatalf("client-supplied country survived: %v", got)
	}
	if got := r.Header.Values(api.ClientUAFamilyHeader); len(got) != 0 {
		t.Fatalf("empty user agent produced a family header: %v", got)
	}
}

func TestClientAttributeHeadersAreReservedIdentity(t *testing.T) {
	for _, name := range []string{api.ClientCountryHeader, api.ClientUAFamilyHeader} {
		if !api.IsGuestIdentityHeader(name) {
			t.Errorf("%s is not a reserved guest identity header", name)
		}
	}
}
