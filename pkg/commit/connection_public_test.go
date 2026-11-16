// adr: 20261112140000
package commit

import (
	"net/netip"
	"strings"
	"testing"
)

func TestNetworkPolicyPublicInternet(t *testing.T) {
	allow := NetworkPolicy{Hosts: map[string]bool{"db.internal": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	public := allow
	public.PublicInternet = true
	for _, tc := range []struct {
		name   string
		policy NetworkPolicy
		host   string
		ip     string
		want   bool
	}{
		{"allowlist keeps its prefixes", public, "db.internal", "10.1.2.3", true},
		{"allowlist host stays off the public rule", public, "db.internal", "8.8.8.8", false},
		{"public name, public address", public, "db.example.com", "8.8.8.8", true},
		{"public name, public v6", public, "db.example.com", "2606:4700::1111", true},
		{"public name, private", public, "db.example.com", "10.1.2.3", false},
		{"public name, loopback", public, "db.example.com", "127.0.0.1", false},
		{"public name, metadata", public, "db.example.com", "169.254.169.254", false},
		{"public name, CGNAT", public, "db.example.com", "100.64.1.1", false},
		{"public name, NAT64 of private", public, "db.example.com", "64:ff9b::a01:203", false},
		{"public name, mapped private", public, "db.example.com", "::ffff:10.1.2.3", false},
		{"public off", allow, "db.example.com", "8.8.8.8", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.permits(tc.host, netip.MustParseAddr(tc.ip)); got != tc.want {
				t.Fatalf("permits(%s, %s) = %v, want %v", tc.host, tc.ip, got, tc.want)
			}
		})
	}
}

func TestOpenPoolPublicRefusesLiteralsAndPrivateNames(t *testing.T) {
	public := NetworkPolicy{PublicInternet: true}
	for _, raw := range []string{
		"postgres://relay:pw@8.8.8.8/shop?sslmode=verify-full",
		"postgres://relay:pw@localhost/shop?sslmode=verify-full",
	} {
		pool, err := OpenPool(t.Context(), raw, public)
		if err == nil {
			pool.Close()
			t.Fatalf("%s: opened", raw)
		}
		if strings.Contains(err.Error(), "pw") {
			t.Fatalf("error leaks credentials: %v", err)
		}
	}
	if _, err := OpenPool(t.Context(), "postgres://relay:pw@db.example.com/shop?sslmode=verify-full", NetworkPolicy{}); err == nil {
		t.Fatal("public name opened without the public rule")
	}
}
