package manifest

import (
	"strings"
	"testing"
)

func TestHostPrivateAddress(t *testing.T) {
	cases := []struct {
		host Host
		want string
	}{
		{Host{Address: "fsn-1.gregale.dev:7100", PrivateIPv4: "10.42.0.1"}, "10.42.0.1"},
		{Host{Address: "10.42.0.3:7100"}, "10.42.0.3"},
		{Host{Address: "fsn-1.gregale.dev:7100"}, ""},
		{Host{Address: "[fd00::1]:7100"}, ""},
		{Host{}, ""},
	}
	for _, c := range cases {
		if got := c.host.PrivateAddress(); got != c.want {
			t.Errorf("PrivateAddress(%+v) = %q, want %q", c.host, got, c.want)
		}
	}
}

func TestValidatePrivateAddresses(t *testing.T) {
	static := Overlay{CIDR: "10.42.0.0/24"}
	bareMetal := Overlay{CIDR: "10.42.0.0/24", Interface: "eno2"}
	cases := []struct {
		name    string
		overlay Overlay
		hosts   []Host
		want    string
	}{
		{"optional without interface", static, []Host{{Name: "a", Address: "a.example:1"}}, ""},
		{"required with interface", bareMetal, []Host{{Name: "a", Address: "a.example:1"}}, "is required"},
		{"ok with interface", bareMetal, []Host{{Name: "a", Address: "a.example:1", PrivateIPv4: "10.42.0.1"}}, ""},
		{"not ipv4", static, []Host{{Name: "a", PrivateIPv4: "fd00::1"}}, "not an IPv4"},
		{"garbage", static, []Host{{Name: "a", PrivateIPv4: "nope"}}, "not an IPv4"},
		{"outside overlay", static, []Host{{Name: "a", PrivateIPv4: "10.43.0.1"}}, "outside overlay.cidr"},
		{"differs from address", static, []Host{{Name: "a", Address: "10.42.0.2:1", PrivateIPv4: "10.42.0.1"}}, "differs from"},
		{"matches address", static, []Host{{Name: "a", Address: "10.42.0.1:1", PrivateIPv4: "10.42.0.1"}}, ""},
		{"duplicate", static, []Host{{Name: "a", PrivateIPv4: "10.42.0.1"}, {Name: "b", PrivateIPv4: "10.42.0.1"}}, "also a's"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validatePrivateAddresses(c.overlay, c.hosts)
			if c.want == "" {
				if len(errs) != 0 {
					t.Fatalf("unexpected errors: %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs.Error(), c.want) {
				t.Fatalf("errors %v, want %q", errs, c.want)
			}
		})
	}
}
