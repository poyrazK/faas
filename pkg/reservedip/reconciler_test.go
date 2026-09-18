package reservedip

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/networkip"
	"github.com/onebox-faas/faas/pkg/state"
)

type recordingConnector struct {
	routes []Route
	err    error
}

func (c *recordingConnector) Apply(_ context.Context, routes []Route) error {
	c.routes = append(c.routes[:0], routes...)
	return c.err
}

func TestReconcilerAssignsPendingLeaseOnActiveNode(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	appID := uuid.NewString()
	lease, err := store.UpsertReservedIP(ctx, state.ReservedIP{
		AccountID: uuid.NewString(), Region: "local", Address: netip.MustParseAddr("198.51.100.24"),
		Status: networkip.StatusPending, AppID: appID,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &recordingConnector{}
	reconciler, err := NewReconciler(store, connector, ReconcilerOptions{Region: "local"})
	if err != nil {
		t.Fatal(err)
	}
	summary, err := reconciler.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Routes != 1 || summary.Assigned != 1 || len(connector.routes) != 1 {
		t.Fatalf("summary=%+v routes=%+v", summary, connector.routes)
	}
	got, err := store.GetReservedIP(ctx, lease.AccountID, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != networkip.StatusAssigned || got.NodeID == "" || got.Generation != 2 {
		t.Fatalf("lease after reconcile=%+v", got)
	}
	if connector.routes[0].NodeID != got.NodeID || connector.routes[0].Generation != 1 {
		t.Fatalf("route=%+v lease=%+v", connector.routes[0], got)
	}
}

func TestReconcilerMovesLeaseAfterNodeFailure(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	region := "fra1"
	for _, id := range []string{"node-a", "node-b"} {
		if _, err := store.CreateComputeNode(ctx, state.ComputeNode{ID: id, Name: id, Active: true, Region: &region}); err != nil {
			t.Fatal(err)
		}
	}
	accountID, appID := uuid.NewString(), uuid.NewString()
	lease, err := store.UpsertReservedIP(ctx, state.ReservedIP{
		AccountID: accountID, Region: region, Address: netip.MustParseAddr("203.0.113.80"),
		Status: networkip.StatusPending, AppID: appID, NodeID: "node-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &recordingConnector{}
	reconciler, err := NewReconciler(store, connector, ReconcilerOptions{Region: region})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	assigned, err := store.GetReservedIP(ctx, accountID, lease.ID)
	if err != nil || assigned.Status != networkip.StatusAssigned || assigned.NodeID != "node-a" {
		t.Fatalf("initial lease=%+v err=%v", assigned, err)
	}
	if err := store.SetComputeNodeActive(ctx, "node-a", false); err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	moved, err := store.GetReservedIP(ctx, accountID, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Status != networkip.StatusAssigned || moved.NodeID != "node-b" || moved.Generation != assigned.Generation+2 {
		t.Fatalf("moved lease=%+v initial=%+v", moved, assigned)
	}
	if len(connector.routes) != 1 || connector.routes[0].NodeID != "node-b" {
		t.Fatalf("routes after failover=%+v", connector.routes)
	}
}

func TestReconcilerFailsClosedWhenConnectorRejectsRoutes(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	accountID, appID := uuid.NewString(), uuid.NewString()
	lease, err := store.UpsertReservedIP(ctx, state.ReservedIP{
		AccountID: accountID, Region: "local", Address: netip.MustParseAddr("198.51.100.31"),
		Status: networkip.StatusPending, AppID: appID,
	})
	if err != nil {
		t.Fatal(err)
	}
	connector := &recordingConnector{err: errors.New("fabric unavailable")}
	reconciler, err := NewReconciler(store, connector, ReconcilerOptions{Region: "local"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reconciler.Sweep(ctx); err == nil {
		t.Fatal("connector failure was swallowed")
	}
	got, err := store.GetReservedIP(ctx, accountID, lease.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != networkip.StatusError || got.NodeID == "" {
		t.Fatalf("failed lease=%+v", got)
	}
}
