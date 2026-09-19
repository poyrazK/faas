package privatenetwork

import (
	"context"
	"net/netip"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestStoreConnectorChecksGregaleNetworkDefinition(t *testing.T) {
	store := state.NewMemStore()
	network, err := store.CreatePrivateNetwork(context.Background(), state.PrivateNetwork{
		AccountID: "acct", Name: "prod", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/28"),
		AllowedCIDRs: []netip.Prefix{netip.MustParsePrefix("10.42.0.0/30")},
	})
	if err != nil {
		t.Fatalf("CreatePrivateNetwork: %v", err)
	}
	connector, err := NewStoreConnector(store)
	if err != nil {
		t.Fatalf("NewStoreConnector: %v", err)
	}
	check, err := connector.Check(context.Background(), state.AppPrivateNetworkAttachment{
		AccountID: "acct", NetworkID: network.ID, Region: network.Region, CIDRs: []netip.Prefix{network.CIDR}, Status: api.PrivateNetworkAttachmentStatusPending,
	})
	if err != nil || !check.Ready {
		t.Fatalf("Check = %+v, err=%v; want ready", check, err)
	}
	if len(check.AllowedCIDRs) != 1 || check.AllowedCIDRs[0].String() != "10.42.0.0/30" {
		t.Fatalf("Check policy = %v", check.AllowedCIDRs)
	}
	check, err = connector.Check(context.Background(), state.AppPrivateNetworkAttachment{AccountID: "acct", NetworkID: "net-missing", Region: "fra1", CIDRs: []netip.Prefix{network.CIDR}})
	if err != nil || check.Ready {
		t.Fatalf("missing Check = %+v, err=%v; want pending", check, err)
	}
}
