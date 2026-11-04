package state

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
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
