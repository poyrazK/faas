package vmmdgrpc

import (
	"context"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
)

func TestToColdBootRequestPrivateNetworkCIDRs(t *testing.T) {
	request, err := toColdBootRequest(context.Background(), &vmmdpb.CreateColdBootRequest{
		Instance: "i-private",
		App: &vmmdpb.AppSpec{
			BaseKey: "/base", LayerKey: "/layer",
			PrivateNetworkCidrs: []string{"10.42.0.0/16"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.PrivateNetworkCIDRs) != 1 || request.PrivateNetworkCIDRs[0] != "10.42.0.0/16" {
		t.Fatalf("private network CIDRs did not cross vmmd adapter: %v", request.PrivateNetworkCIDRs)
	}
}
