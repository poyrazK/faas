package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrivateNetworkAttachmentNodeStatusMemStoreMergesStages(t *testing.T) {
	store := NewMemStore()
	ctx := context.Background()
	observedAt := time.Date(2026, time.September, 22, 12, 0, 0, 0, time.UTC)
	if err := store.UpsertPrivateNetworkAttachmentNodeStatus(ctx, PrivateNetworkAttachmentNodeStatus{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "net-1", NodeID: "node-b",
		FabricStatus: "ready", FabricDetail: "bridge ready", ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("fabric observation: %v", err)
	}
	if err := store.UpsertPrivateNetworkAttachmentNodeStatus(ctx, PrivateNetworkAttachmentNodeStatus{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "net-1", NodeID: "node-b",
		RouteStatus: "error", RouteDetail: "route update failed", ObservedAt: observedAt.Add(time.Second),
	}); err != nil {
		t.Fatalf("route observation: %v", err)
	}
	if err := store.UpsertPrivateNetworkAttachmentNodeStatus(ctx, PrivateNetworkAttachmentNodeStatus{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "net-1", NodeID: "node-a",
		RouteStatus: "ready", RouteDetail: "routes applied", ObservedAt: observedAt,
	}); err != nil {
		t.Fatalf("second node observation: %v", err)
	}

	rows, err := store.ListPrivateNetworkAttachmentNodeStatuses(ctx, "acct-1", "app-1")
	if err != nil {
		t.Fatalf("list observations: %v", err)
	}
	if len(rows) != 2 || rows[0].NodeID != "node-a" || rows[1].NodeID != "node-b" {
		t.Fatalf("observations not sorted by node: %#v", rows)
	}
	if rows[1].FabricStatus != "ready" || rows[1].RouteStatus != "error" {
		t.Fatalf("stage merge lost data: %#v", rows[1])
	}
	if err := store.UpsertPrivateNetworkAttachmentNodeStatus(ctx, PrivateNetworkAttachmentNodeStatus{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "net-2", NodeID: "node-b", RouteStatus: "ready",
	}); err != nil {
		t.Fatalf("network replacement observation: %v", err)
	}
	rows, err = store.ListPrivateNetworkAttachmentNodeStatuses(ctx, "acct-1", "app-1")
	if err != nil || rows[1].NetworkID != "net-2" || rows[1].FabricStatus != "" {
		t.Fatalf("network replacement kept stale stage: rows=%#v err=%v", rows, err)
	}
	if err := store.DeletePrivateNetworkAttachmentNodeStatuses(ctx, "acct-1", "app-1"); err != nil {
		t.Fatalf("delete observations: %v", err)
	}
	rows, err = store.ListPrivateNetworkAttachmentNodeStatuses(ctx, "acct-1", "app-1")
	if err != nil || len(rows) != 0 {
		t.Fatalf("observations remain after delete: rows=%#v err=%v", rows, err)
	}
}

func TestPrivateNetworkAttachmentNodeStatusRejectsUnknownStage(t *testing.T) {
	store := NewMemStore()
	err := store.UpsertPrivateNetworkAttachmentNodeStatus(context.Background(), PrivateNetworkAttachmentNodeStatus{
		AccountID: "acct-1", AppID: "app-1", NetworkID: "net-1", NodeID: "node-a", RouteStatus: "pending",
	})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("unknown stage status error = %v, want %v", err, ErrInvalidArgument)
	}
}
