// adr: 20261104143000
package vmmdgrpc_test

import (
	"context"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type peeringFirewallVMM struct {
	vmmdgrpc.VmmdAPI
	updates int
	cidrs   []netip.Prefix
	rules   []api.PrivateNetworkFirewallRule
}

func (v *peeringFirewallVMM) UpdatePrivateNetwork(context.Context, string, []netip.Prefix) error {
	return nil
}
func (v *peeringFirewallVMM) UpdatePrivateNetworkAttachmentWithFirewall(_ context.Context, _, _ string, _ netip.Addr, cidrs, _ []netip.Prefix, rules []api.PrivateNetworkFirewallRule) error {
	v.updates++
	v.cidrs = cidrs
	v.rules = rules
	return nil
}

func (v *peeringFirewallVMM) UpdatePrivateNetworkWithFirewall(_ context.Context, _ string, cidrs, _ []netip.Prefix, rules []api.PrivateNetworkFirewallRule) error {
	v.updates++
	v.cidrs = cidrs
	v.rules = rules
	return nil
}

func TestPrivateNetworkFirewallRPCWithPeering(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cidrs     []string
		member    string
		ruleCIDRs []string
		valid     bool
	}{
		{"legacy-single-subnet", []string{"10.43.0.0/24"}, "", []string{"10.43.0.0/25"}, true},
		{"single-subnet", []string{"10.43.0.0/24"}, "10.43.0.2", []string{"10.43.0.0/25"}, true},
		{"peer-before-member", []string{"10.42.0.0/24", "10.43.0.0/24"}, "10.43.0.2", []string{"10.43.0.0/25"}, true},
		{"member-before-peer", []string{"10.43.0.0/24", "10.42.0.0/24"}, "10.43.0.2", nil, true},
		{"peer-rule-not-owning-baseline", []string{"10.42.0.0/24", "10.43.0.0/24"}, "10.43.0.2", []string{"10.42.0.0/25"}, false},
		{"member-outside-routes", []string{"10.42.0.0/24", "10.43.0.0/24"}, "10.44.0.2", nil, false},
		{"invalid-member", []string{"10.42.0.0/24", "10.43.0.0/24"}, "bad", nil, false},
		{"ipv6-member", []string{"10.42.0.0/24", "10.43.0.0/24"}, "fd00::2", nil, false},
		{"ambiguous-no-member", []string{"10.42.0.0/24", "10.43.0.0/24"}, "", nil, false},
		{"no-routes", nil, "10.43.0.2", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := &peeringFirewallVMM{}
			req := &vmmdpb.UpdatePrivateNetworkRequest{AppId: "app-firewall", PrivateNetworkId: "prod", PrivateNetworkAddress: tc.member, PrivateNetworkCidrs: tc.cidrs,
				PrivateNetworkFirewallRules: []*vmmdpb.PrivateNetworkFirewallRule{{Direction: "egress", Protocol: "tcp", Ports: []string{"443"}, Cidrs: tc.ruleCIDRs}}}
			if tc.member == "" && len(tc.cidrs) == 1 {
				req.PrivateNetworkId = ""
			}
			_, err := vmmdgrpc.New(v, nil, "", nil).UpdatePrivateNetwork(t.Context(), req)
			if !tc.valid {
				if status.Code(err) != codes.InvalidArgument || v.updates != 0 {
					t.Fatalf("invalid policy reached manager: updates=%d err=%v", v.updates, err)
				}
				return
			}
			if err != nil || v.updates != 1 || len(v.cidrs) != len(tc.cidrs) || len(v.rules) != 1 {
				t.Fatalf("valid firewall/peering update failed: updates=%d err=%v", v.updates, err)
			}
			if len(v.rules[0].CIDRs) != len(tc.ruleCIDRs) {
				t.Fatalf("omitted scope was widened to peer routes: %+v", v.rules)
			}
		})
	}
}
