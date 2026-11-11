package netns

import (
	"net/netip"
	"strings"
	"testing"
)

func TestHostPrivateFabricPermitsOnlySameBridgeAndIsolatesConntrack(t *testing.T) {
	policy := DefaultHostPolicy
	policy.PrivateNetworkBridges = []string{"gpn-1111111111", "gpn-2222222222"}
	rendered := policy.Render()
	for _, bridge := range policy.PrivateNetworkBridges {
		if !strings.Contains(rendered, "iifname \""+bridge+"\" notrack") {
			t.Fatal("private bridge shares root conntrack state")
		}
		pair := "iifname \"" + bridge + "\" oifname \"" + bridge + "\" accept"
		if !strings.Contains(rendered, pair) {
			t.Fatalf("same-bridge forwarding is blocked: %s", pair)
		}
	}
	if strings.Contains(rendered, "iifname \"gpn-1111111111\" oifname \"gpn-2222222222\" accept") {
		t.Fatal("root policy admits cross-network routing")
	}
	forward := strings.Index(rendered, "chain forward {")
	allow := strings.Index(rendered, "oifname \"gpn-1111111111\" accept")
	deny := strings.Index(rendered[forward:], "ip daddr 10.0.0.0/8") + forward
	if forward >= allow || allow >= deny {
		t.Fatal("private bridge exemption must be within forward, before the host private-address deny")
	}
}

func TestRevokedStaticEgressBlocksOutboundBeforeEstablishedAccept(t *testing.T) {
	policy := DefaultHostPolicy
	policy.StaticEgressBlockedSources = []netip.Addr{netip.MustParseAddr("10.100.0.77")}
	rendered := policy.Render()
	forward := rendered[strings.Index(rendered, "chain forward {"):]
	blocked := strings.Index(forward, "ip saddr 10.100.0.77 ct direction original drop")
	if blocked < 0 || blocked > strings.Index(forward, "ct state established,related accept") {
		t.Fatal("revocation allowed established outbound connections to keep the removed identity")
	}
}
