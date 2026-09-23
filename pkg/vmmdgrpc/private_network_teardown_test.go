package vmmdgrpc_test

import (
	"context"
	"net/netip"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

type privateNetworkTeardownVMM struct {
	vmmdgrpc.VmmdAPI
	called bool
	args   struct {
		accountID string
		networkID string
		region    string
		cidr      netip.Prefix
	}
}

func (v *privateNetworkTeardownVMM) RemovePrivateNetworkFabric(_ context.Context, accountID, networkID, region string, cidr netip.Prefix) error {
	v.called = true
	v.args.accountID = accountID
	v.args.networkID = networkID
	v.args.region = region
	v.args.cidr = cidr
	return nil
}

var _ vmmdgrpc.VmmdAPI = (*privateNetworkTeardownVMM)(nil)

func TestRemovePrivateNetworkFabricValidatesAndForwardsIdentity(t *testing.T) {
	vmm := &privateNetworkTeardownVMM{}
	server := vmmdgrpc.New(vmm, nil, "", nil)
	_, err := server.RemovePrivateNetworkFabric(context.Background(), &vmmdpb.RemovePrivateNetworkFabricRequest{
		AccountId: "acct-1", NetworkId: "net-1", Region: "fra1", Cidr: "10.42.0.0/24",
	})
	if err != nil {
		t.Fatalf("RemovePrivateNetworkFabric: %v", err)
	}
	if !vmm.called || vmm.args.accountID != "acct-1" || vmm.args.networkID != "net-1" || vmm.args.region != "fra1" || vmm.args.cidr != netip.MustParsePrefix("10.42.0.0/24") {
		t.Fatalf("teardown args = %+v, called=%v", vmm.args, vmm.called)
	}
}

func TestRemovePrivateNetworkFabricRejectsInvalidCIDR(t *testing.T) {
	vmm := &privateNetworkTeardownVMM{}
	server := vmmdgrpc.New(vmm, nil, "", nil)
	if _, err := server.RemovePrivateNetworkFabric(context.Background(), &vmmdpb.RemovePrivateNetworkFabricRequest{
		AccountId: "acct-1", NetworkId: "net-1", Region: "fra1", Cidr: "not-a-cidr",
	}); err == nil {
		t.Fatal("RemovePrivateNetworkFabric accepted invalid CIDR")
	}
	if vmm.called {
		t.Fatal("RemovePrivateNetworkFabric called VMM for invalid CIDR")
	}
}
