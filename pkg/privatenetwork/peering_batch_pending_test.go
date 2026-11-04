package privatenetwork

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"testing"
)

func TestPeeringBatchDoesNotStarvePending(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	var last state.PrivateNetworkPeering
	for i := 0; i < 101; i++ {
		account := fmt.Sprintf("acct-%03d", i)
		left, right := fmt.Sprintf("left-%03d", i), fmt.Sprintf("right-%03d", i)
		for _, network := range []state.PrivateNetwork{
			{ID: left, Name: "left", AccountID: account, Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")},
			{ID: right, Name: "right", AccountID: account, Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/24")},
		} {
			if _, err := store.CreatePrivateNetwork(ctx, network); err != nil {
				t.Fatal(err)
			}
		}
		status := api.PrivateNetworkPeeringStatusReady
		if i == 100 {
			status = api.PrivateNetworkPeeringStatusPending
		}
		var err error
		last, err = store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{ID: fmt.Sprintf("peer-%03d", i), AccountID: account, Region: "fra1", LeftNetworkID: left, RightNetworkID: right, Status: status})
		if err != nil {
			t.Fatal(err)
		}
	}
	applier := &recordingPeeringApplier{}
	r, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err = r.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	p, err := store.GetPrivateNetworkPeering(ctx, last.AccountID, last.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != api.PrivateNetworkPeeringStatusReady {
		t.Fatalf("after five successful default sweeps, peering 101 is still %q: the same 100 ready rows monopolize the oldest-first batch", p.Status)
	}
}
