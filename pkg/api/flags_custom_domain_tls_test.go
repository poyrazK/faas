package api

import (
	"net/netip"
	"slices"
	"testing"
)

func TestCustomDomainTLSOnDemand(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  bool
	}{
		{"", false},
		{"on_demand", true},
		{" ON_DEMAND ", true},
		{"dns01", false},
		{"1", false},
	} {
		t.Setenv("FAAS_CUSTOM_DOMAIN_TLS", tc.value)
		if got := CustomDomainTLSOnDemand(); got != tc.want {
			t.Errorf("CustomDomainTLSOnDemand(%q) = %v, want %v", tc.value, got, tc.want)
		}
	}
}

func TestCustomDomainTargetNormalizes(t *testing.T) {
	t.Setenv("FAAS_CUSTOM_DOMAIN_TARGET", "  Edge.Gregale.dev. ")
	if got := CustomDomainTarget(); got != "edge.gregale.dev" {
		t.Fatalf("CustomDomainTarget() = %q, want edge.gregale.dev", got)
	}
}

func TestCustomDomainAddresses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    []netip.Addr
		wantErr bool
	}{
		{name: "unset", value: ""},
		{name: "ipv4 and ipv6", value: "203.0.113.10, 2001:db8::1", want: []netip.Addr{
			netip.MustParseAddr("203.0.113.10"), netip.MustParseAddr("2001:db8::1"),
		}},
		{name: "mapped ipv4 unmapped", value: "::ffff:203.0.113.10", want: []netip.Addr{netip.MustParseAddr("203.0.113.10")}},
		{name: "empty entries skipped", value: "203.0.113.10,,", want: []netip.Addr{netip.MustParseAddr("203.0.113.10")}},
		{name: "invalid entry", value: "203.0.113.10,edge.gregale.dev", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("FAAS_CUSTOM_DOMAIN_ADDRESSES", tc.value)
			got, err := CustomDomainAddresses()
			if (err != nil) != tc.wantErr {
				t.Fatalf("CustomDomainAddresses() err = %v, wantErr %v", err, tc.wantErr)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("CustomDomainAddresses() = %v, want %v", got, tc.want)
			}
		})
	}
}
