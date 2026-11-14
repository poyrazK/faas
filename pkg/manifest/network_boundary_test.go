package manifest

import (
	"strings"
	"testing"
)

// ADR-20261109100000: overlay.interface names the private card for the
// static provider only, and ssh.allowed_cidrs never admits everyone.
func TestNetworkBoundaryValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		overlay Overlay
		ssh     *SSH
		want    string // substring of the error; empty means valid
	}{
		{name: "cloud default", overlay: Overlay{Provider: "static", CIDR: "10.10.0.0/20"}},
		{name: "bare metal", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno2"},
			ssh: &SSH{AllowedCIDRs: []string{"198.51.100.10/32", "2001:db8::/64"}}},
		{name: "private network only", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24"}, ssh: &SSH{}},
		{name: "vlan card", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno2.4000"}},
		{name: "bad card", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "eno 2"}, want: "overlay.interface"},
		{name: "long card", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24", Interface: "a-very-long-name0"}, want: "overlay.interface"},
		{name: "card on wireguard", overlay: Overlay{Provider: "wireguard", CIDR: "10.20.0.0/24", Interface: "eno2"}, want: "static provider only"},
		{name: "bad ssh cidr", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24"}, ssh: &SSH{AllowedCIDRs: []string{"198.51.100.10"}}, want: "ssh.allowed_cidrs[0]"},
		{name: "ssh open to all", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24"}, ssh: &SSH{AllowedCIDRs: []string{"0.0.0.0/0"}}, want: "admits every address"},
		{name: "ssh open to all v6", overlay: Overlay{Provider: "static", CIDR: "10.20.0.0/24"}, ssh: &SSH{AllowedCIDRs: []string{"::/0"}}, want: "admits every address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errs := append(tc.overlay.validate(), tc.ssh.validate()...)
			got := errs.Error()
			if tc.want == "" && len(errs) > 0 {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Fatalf("want error containing %q, got %q", tc.want, got)
			}
		})
	}
}
