package privatenetwork

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPersistNodeObservationsFoldsFabricAndRouteReports(t *testing.T) {
	store := state.NewMemStore()
	err := PersistNodeObservations(context.Background(), store, ReconcileObservation{
		AccountID: "acct-1", NetworkID: "net-1", AppID: "app-1",
		FabricNodes: []RouteNodeObservation{
			{NodeID: "node-a", Status: "ready", Detail: "bridge ready"},
			{NodeID: "node-b", Status: "error", Detail: "bridge failed"},
		},
		Nodes: []RouteNodeObservation{
			{NodeID: "node-a", Status: "ready", Detail: "routes applied"},
			{NodeID: "node-b", Status: "error", Detail: "route failed"},
		},
	})
	if err != nil {
		t.Fatalf("persist observations: %v", err)
	}
	rows, err := store.ListPrivateNetworkAttachmentNodeStatuses(context.Background(), "acct-1", "app-1")
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) != 2 || rows[0].NodeID != "node-a" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	if rows[0].FabricStatus != "ready" || rows[0].RouteStatus != "ready" || rows[1].FabricStatus != "error" || rows[1].RouteStatus != "error" {
		t.Fatalf("unexpected stage status: %#v", rows)
	}
}
