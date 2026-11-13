// adr: 20261104160000
package vmmdgrpc_test

import (
	"context"
	"errors"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type restrictiveRouterVMM struct{ privateRouterVMM }

func (v *restrictiveRouterVMM) RestrictPrivateNetworkPeerings(ctx context.Context, account, region string, networks []privatenetwork.FabricSpec, routes []privatenetwork.PeeringRoute) error {
	return v.ReconcilePrivateNetworkPeerings(ctx, account, region, networks, routes)
}

func TestPrivateNetworkRestrictionRequiresCapabilityAndAcknowledgement(t *testing.T) {
	req := &vmmdpb.ReconcilePrivateNetworkPeeringsRequest{AccountId: "account", Region: "fra1"}
	legacy := vmmdgrpc.New(&privateRouterVMM{}, nil, "", nil)
	if _, err := legacy.RestrictPrivateNetworkPeerings(t.Context(), req); status.Code(err) != codes.Unimplemented {
		t.Fatalf("legacy manager acknowledged revocation: %v", err)
	}
	vmm := &restrictiveRouterVMM{}
	server := vmmdgrpc.New(vmm, nil, "", nil)
	if ack, err := server.RestrictPrivateNetworkPeerings(t.Context(), req); err != nil || ack == nil || vmm.calls != 1 {
		t.Fatalf("valid restriction failed: ack=%v err=%v calls=%d", ack, err, vmm.calls)
	}
	vmm.err = errors.New("nft failed")
	if ack, err := server.RestrictPrivateNetworkPeerings(t.Context(), req); ack != nil || status.Code(err) != codes.Unavailable {
		t.Fatalf("failed restriction acknowledged: ack=%v err=%v", ack, err)
	}
	before := vmm.calls
	req.Networks = []*vmmdpb.PrivateNetworkRouterNetwork{{NetworkId: "network", Cidr: "bad-cidr"}}
	if _, err := server.RestrictPrivateNetworkPeerings(t.Context(), req); status.Code(err) != codes.InvalidArgument || vmm.calls != before {
		t.Fatalf("invalid restriction mutated host: err=%v calls=%d", err, vmm.calls)
	}
}
