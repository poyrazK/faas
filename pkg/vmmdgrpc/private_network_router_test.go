package vmmdgrpc_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type privateRouterVMM struct {
	vmmdgrpc.VmmdAPI
	calls    int
	err      error
	networks []privatenetwork.FabricSpec
}

func (v *privateRouterVMM) ReconcilePrivateNetworkPeerings(_ context.Context, _, _ string, networks []privatenetwork.FabricSpec, _ []privatenetwork.PeeringRoute) error {
	v.calls++
	v.networks = networks
	return v.err
}

func TestPrivateNetworkPeeringRPCValidatesBeforeMutationAndPropagatesFailure(t *testing.T) {
	for _, scenario := range []string{"valid", "invalid-cidr", "one-way", "failed-apply"} {
		t.Run(scenario, func(t *testing.T) {
			vmm := &privateRouterVMM{}
			req := &vmmdpb.ReconcilePrivateNetworkPeeringsRequest{AccountId: "acct-rpc", Region: "fra1",
				Networks: []*vmmdpb.PrivateNetworkRouterNetwork{{NetworkId: "left", Cidr: "10.42.0.0/24"}, {NetworkId: "right", Cidr: "10.43.0.0/24"}},
				Routes: []*vmmdpb.PrivateNetworkRouterRoute{
					{FromNetworkId: "left", DestinationNetworkId: "right", DestinationCidr: "10.43.0.0/24"},
					{FromNetworkId: "right", DestinationNetworkId: "left", DestinationCidr: "10.42.0.0/24"},
				},
			}
			switch scenario {
			case "invalid-cidr":
				req.Networks[0].Cidr = "not-a-cidr"
			case "one-way":
				req.Routes = req.Routes[:1]
			case "failed-apply":
				vmm.err = errors.New("nft transaction failed")
			}
			ack, err := vmmdgrpc.New(vmm, nil, "", nil).ReconcilePrivateNetworkPeerings(t.Context(), req)
			if scenario == "invalid-cidr" || scenario == "one-way" {
				if status.Code(err) != codes.InvalidArgument || vmm.calls != 0 {
					t.Fatalf("invalid policy reached host: calls=%d error=%v", vmm.calls, err)
				}
				return
			}
			if scenario == "failed-apply" {
				if status.Code(err) != codes.Unavailable || ack != nil {
					t.Fatalf("failed apply acknowledged: ack=%v error=%v", ack, err)
				}
				return
			}
			if err != nil || ack == nil || vmm.calls != 1 {
				t.Fatalf("valid policy failed: ack=%v calls=%d error=%v", ack, vmm.calls, err)
			}
			if vmm.networks[0].AccountID != "acct-rpc" || vmm.networks[0].Region != "fra1" || vmm.networks[0].CIDR != netip.MustParsePrefix("10.42.0.0/24") {
				t.Fatalf("wire policy scope lost: %+v", vmm.networks)
			}
		})
	}
}
