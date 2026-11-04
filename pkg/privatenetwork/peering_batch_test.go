package privatenetwork

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"net/netip"
	"testing"
)

func TestPeeringBatchRetainsCompleteGroup(t *testing.T) {
	ctx := t.Context()
	store := state.NewMemStore()
	for i := 0; i < 15; i++ {
		_, err := store.CreatePrivateNetwork(ctx, state.PrivateNetwork{ID: fmt.Sprintf("net-%02d", i), Name: fmt.Sprintf("net-%02d", i), AccountID: "acct-r2", Region: "fra1", CIDR: netip.MustParsePrefix(fmt.Sprintf("10.%d.0.0/24", i+40))})
		if err != nil {
			t.Fatal(err)
		}
	}
	count := 0
	for i := 0; i < 15; i++ {
		for j := i + 1; j < 15 && count < 101; j++ {
			_, err := store.CreatePrivateNetworkPeering(ctx, state.PrivateNetworkPeering{ID: fmt.Sprintf("peer-%03d", count), AccountID: "acct-r2", Region: "fra1", LeftNetworkID: fmt.Sprintf("net-%02d", i), RightNetworkID: fmt.Sprintf("net-%02d", j), Status: api.PrivateNetworkPeeringStatusReady})
			if err != nil {
				t.Fatal(err)
			}
			count++
		}
	}
	applier := &recordingPeeringApplier{}
	r, err := NewPeeringReconciler(store, applier, PeeringReconcilerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.SweepAccountRegion(ctx, "acct-r2", "fra1"); err != nil {
		t.Fatal(err)
	}
	if len(applier.calls[0].routes) != 202 {
		t.Fatal("fixture did not install all peerings")
	}
	if _, err = r.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(applier.calls[1].routes); got != 202 {
		t.Fatalf("periodic sweep replaces 202 routes with %d from truncated group; omitted peering remains ready", got)
	}
}
