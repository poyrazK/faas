package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"net/netip"
	"testing"
)

func TestPrivateTransportCollisionReservations(t *testing.T) {
	ctx := context.Background()
	m := NewMemStore()
	accounts := []string{"20a81785-db2a-457e-b60d-a7023dfa2268", "51eb7ea1-d841-4707-9bda-916bbbe7101f"}
	networks := []string{"net-c765e790-f0f1-436b-bc29-f74226a164fb", "net-07ab920b-a929-47eb-abdb-4e98a2dcc340"}
	if PreferredPrivateNetworkVNI(accounts[0], networks[0]) != PreferredPrivateNetworkVNI(accounts[1], networks[1]) {
		t.Fatal("fixture is not a collision")
	}
	m.mu.Lock()
	for i, id := range networks {
		m.privateNetworks[id] = PrivateNetwork{ID: id, AccountID: accounts[i], Region: "test"}
	}
	m.mu.Unlock()
	a, err := m.EnsurePrivateNetworkTransportVNI(ctx, accounts[0], networks[0], "test")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.EnsurePrivateNetworkTransportVNI(ctx, accounts[1], networks[1], "test")
	if err != nil {
		t.Fatal(err)
	}
	if a == b || a == 0 || b > api.PrivateNetworkMaxTransportVNI {
		t.Fatalf("colliding allocations %d %d", a, b)
	}
	again, err := m.EnsurePrivateNetworkTransportVNI(ctx, accounts[1], networks[1], "test")
	if err != nil || again != b {
		t.Fatalf("unstable identity %d %v", again, err)
	}
	if err := m.DeletePrivateNetwork(ctx, accounts[0], networks[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.EnsurePrivateNetworkTransportVNI(ctx, accounts[0], networks[0], "test"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted network recreated: %v", err)
	}
	if _, ok := m.privateNetworkTransportIDs[networks[0]]; !ok {
		t.Fatal("deleted reservation was reused")
	}
}

func TestPrivateMemberWildcardReleaseFindsOwnerOnlyClaim(t *testing.T) {
	m := NewMemStore()
	m.privateNetworkServiceOwners = map[string]privateNetworkServiceOwner{privateNetworkServiceKey("network", "app"): {AccountID: "account", AppID: "app", NetworkID: "network", InstanceID: "instance"}}
	if err := m.ReleasePrivateNetworkMember(t.Context(), "", "", "", "instance", false); err != nil {
		t.Fatal(err)
	}
	if len(m.privateNetworkServiceOwners) != 0 {
		t.Fatal("wildcard cleanup ignored owner without an instance address")
	}
}

func TestPrivateMemberWithdrawalIdentityIsOwnerOnly(t *testing.T) {
	m := NewMemStore()
	n, err := m.CreatePrivateNetwork(t.Context(), PrivateNetwork{ID: "net-withdrawal", AccountID: "account", Name: "withdrawal", Region: "fra1", CIDR: netip.MustParsePrefix("10.92.0.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpsertAppPrivateNetworkAttachment(t.Context(), AppPrivateNetworkAttachment{AccountID: "account", AppID: "app", NetworkID: n.ID, Region: n.Region, CIDRs: []netip.Prefix{n.CIDR}}); err != nil {
		t.Fatal(err)
	}
	owner, err := m.EnsurePrivateNetworkMember(t.Context(), "account", "app", n.ID, "owner", true)
	if err != nil {
		t.Fatal(err)
	}
	withdraw, err := m.EnsurePrivateNetworkMember(t.Context(), "account", "app", n.ID, "owner", false)
	if err != nil || withdraw.Service.IsValid() || withdraw.WithdrawService != owner.Service {
		t.Fatalf("owned claim not projected for cleanup: %+v %v", withdraw, err)
	}
	other, err := m.EnsurePrivateNetworkMember(t.Context(), "account", "app", n.ID, "other", false)
	if err != nil || other.Service.IsValid() || other.WithdrawService.IsValid() {
		t.Fatalf("non-owner received service identity: %+v %v", other, err)
	}
}
