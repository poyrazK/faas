package gateway

import "testing"

// A tenant's Set-Cookie with Domain=<apps domain> reached every other
// tenant's app (bug hunt 2026-10-27 H27). It is made host-only; other
// cookies pass unchanged.
func TestScopeTenantCookie(t *testing.T) {
	owned := []string{"gregale.dev", "gregale.app", "deploy.gregale.dev"}
	for _, tc := range []struct{ in, want string }{
		{"sid=1; Path=/; Domain=gregale.app; Secure", "sid=1; Path=/; Secure"},
		{"sid=1; domain=.GREGALE.APP", "sid=1"},
		{"sid=1; Domain=deploy.gregale.dev", "sid=1"},
		{"sid=1; Domain=dev", "sid=1"}, // a parent of an owned domain
		{"sid=1; Path=/; Secure", "sid=1; Path=/; Secure"},
		{"sid=1; Domain=acme.gregale.app", "sid=1; Domain=acme.gregale.app"},
		{"sid=1; Domain=shop.example.com", "sid=1; Domain=shop.example.com"},
		{"sid=1; Domain=example.com", "sid=1; Domain=example.com"},
	} {
		if got := scopeTenantCookieTo(tc.in, owned); got != tc.want {
			t.Errorf("scopeTenantCookieTo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Basic credentials compared with early-returning ConstantTimeCompare and
// ||, so timing revealed the username and both lengths (H5).
func TestBasicCredentialsMatch(t *testing.T) {
	for _, tc := range []struct {
		user, pass string
		want       bool
	}{
		{"admin", "s3cret", true},
		{"admin", "wrong", false},
		{"other", "s3cret", false},
		{"", "", false},
		{"admin", "s3cret-longer", false},
	} {
		if got := basicCredentialsMatch(tc.user, tc.pass, "admin", "s3cret"); got != tc.want {
			t.Errorf("basicCredentialsMatch(%q, %q) = %v, want %v", tc.user, tc.pass, got, tc.want)
		}
	}
}
