package api

import (
	"net/netip"
	"strconv"
	"testing"
)

func TestValidatePrivateNetworkIdentifier(t *testing.T) {
	for _, value := range []string{"prod-vpc", "vpc1", "a"} {
		if err := ValidatePrivateNetworkIdentifier(value); err != nil {
			t.Errorf("%q rejected: %v", value, err)
		}
	}
	for _, value := range []string{"", "Prod-vpc", "_vpc", "vpc_1", "-vpc"} {
		if err := ValidatePrivateNetworkIdentifier(value); err == nil {
			t.Errorf("%q accepted, want rejection", value)
		}
	}
}

func TestValidatePrivateNetworkCIDRsCanonicalizesAndRejectsOverlap(t *testing.T) {
	got, err := ValidatePrivateNetworkCIDRs([]string{"10.20.1.1/16", "10.30.0.0/16"}, 16)
	if err != nil {
		t.Fatalf("valid CIDRs rejected: %v", err)
	}
	if got[0].String() != "10.20.0.0/16" {
		t.Fatalf("first CIDR = %s, want canonical 10.20.0.0/16", got[0])
	}
	for _, values := range [][]string{
		{"10.0.0.0/8"},
		{"10.20.0.0/16", "10.20.1.0/24"},
		{"10.20.0.0/16", "10.20.0.0/16"},
		{"10.20.0.0/16", "2001:db8::/64"},
		{"10.20.0.0/16", "0.0.0.0/0"},
	} {
		if _, err := ValidatePrivateNetworkCIDRs(values, 16); err == nil {
			t.Errorf("CIDRs %v accepted, want rejection", values)
		}
	}
}

func TestValidatePrivateNetworkCIDRsEnforcesMax(t *testing.T) {
	if _, err := ValidatePrivateNetworkCIDRs([]string{"10.20.0.0/16", "10.30.0.0/16"}, 1); err == nil {
		t.Fatal("CIDR cap not enforced")
	}
}

func TestValidatePrivateNetworkPolicyCIDRsIsContainedAndOptional(t *testing.T) {
	destinations := []netip.Prefix{netip.MustParsePrefix("10.42.0.0/16")}
	if got, err := ValidatePrivateNetworkPolicyCIDRs(nil, destinations); err != nil || got != nil {
		t.Fatalf("empty policy = %v, %v; want nil, nil", got, err)
	}
	got, err := ValidatePrivateNetworkPolicyCIDRs([]string{"10.42.8.0/24"}, destinations)
	if err != nil || len(got) != 1 || got[0].String() != "10.42.8.0/24" {
		t.Fatalf("contained policy = %v, %v", got, err)
	}
	if _, err := ValidatePrivateNetworkPolicyCIDRs([]string{"10.43.0.0/16"}, destinations); err == nil {
		t.Fatal("policy outside attached network unexpectedly accepted")
	}
}

func TestValidatePrivateNetworkFirewallRulesCanonicalizesAndValidates(t *testing.T) {
	network := netip.MustParsePrefix("10.42.0.0/16")
	got, err := ValidatePrivateNetworkFirewallRules([]PrivateNetworkFirewallRule{{
		Direction: " INGRESS ",
		Protocol:  " TCP ",
		CIDRs:     []string{"10.42.8.5/24"},
		Ports:     []string{" 443 ", "8000 - 8080"},
	}}, network)
	if err != nil {
		t.Fatalf("valid firewall rule rejected: %v", err)
	}
	if len(got) != 1 || got[0].Direction != "ingress" || got[0].Protocol != "tcp" {
		t.Fatalf("rule identity not canonicalized: %#v", got)
	}
	if got[0].CIDRs[0] != "10.42.8.0/24" || got[0].Ports[0] != "443" || got[0].Ports[1] != "8000-8080" {
		t.Fatalf("rule values not canonicalized: %#v", got[0])
	}

	for _, rule := range []PrivateNetworkFirewallRule{
		{Direction: "ingress", Protocol: "icmp", Ports: []string{"8"}},
		{Direction: "ingress", Protocol: "tcp"},
		{Direction: "ingress", Protocol: "tcp", Ports: []string{"8080-80"}},
		{Direction: "egress", Protocol: "udp", CIDRs: []string{"10.43.0.0/16"}, Ports: []string{"53"}},
	} {
		if _, err := ValidatePrivateNetworkFirewallRules([]PrivateNetworkFirewallRule{rule}, network); err == nil {
			t.Errorf("invalid firewall rule accepted: %#v", rule)
		}
	}
	tooManyCIDRs := make([]string, PrivateNetworkFirewallMaxCIDRsPerRule+1)
	for i := range tooManyCIDRs {
		tooManyCIDRs[i] = "10.42." + strconv.Itoa(i%256) + ".0/24"
	}
	if _, err := ValidatePrivateNetworkFirewallRules([]PrivateNetworkFirewallRule{{Direction: "ingress", Protocol: "icmp", CIDRs: tooManyCIDRs}}, network); err == nil {
		t.Fatal("firewall rule CIDR cap not enforced")
	}
}
