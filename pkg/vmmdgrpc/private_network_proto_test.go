package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

// adr: 009
func TestToColdBootRequestPrivateNetworkCIDRs(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "i-private",
		App: &vmmdpb.AppSpec{
			BaseKey: "/base", LayerKey: "/layer",
			PrivateNetworkCidrs: []string{"10.42.0.0/16"}, PrivateNetworkAllowedCidrs: []string{"10.42.8.0/24"}, PrivateNetworkId: "net-1", PrivateNetworkAddress: "10.42.0.2",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.PrivateNetworkCIDRs) != 1 || request.PrivateNetworkCIDRs[0] != "10.42.0.0/16" {
		t.Fatalf("private network CIDRs did not cross vmmd adapter: %v", request.PrivateNetworkCIDRs)
	}
	if request.PrivateNetworkID != "net-1" || request.PrivateNetworkAddress != "10.42.0.2" {
		t.Fatalf("private network identity did not cross vmmd adapter: id=%q address=%q", request.PrivateNetworkID, request.PrivateNetworkAddress)
	}
	if len(request.PrivateNetworkAllowedCIDRs) != 1 || request.PrivateNetworkAllowedCIDRs[0] != "10.42.8.0/24" {
		t.Fatalf("private network policy did not cross vmmd adapter: %v", request.PrivateNetworkAllowedCIDRs)
	}
}
