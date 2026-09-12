package state

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMemStoreHeartbeatStatsKeepLatestSourceSpecificSamples(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()

	node, err := store.CreateComputeNode(ctx, ComputeNode{
		Name:      "heartbeat-stats-node",
		TargetURL: "unix:///run/faas/vmmd.sock",
		Active:    true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	peer, err := store.CreateComputeNode(ctx, ComputeNode{
		Name:      "heartbeat-stats-peer",
		TargetURL: "unix:///run/faas/vmmd-peer.sock",
		Active:    true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode(peer): %v", err)
	}

	base := time.Date(2026, time.September, 12, 10, 0, 0, 0, time.UTC)
	if err := store.AppendComputeNodeHeartbeatWithStats(ctx, "missing-node", base, base, "heartbeat_tick", 10, 20); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AppendComputeNodeHeartbeatWithStats(missing) = %v, want ErrNotFound", err)
	}
	if err := store.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, base, base, "builder_tick", 12.5, 1024); err != nil {
		t.Fatalf("AppendComputeNodeHeartbeatWithStats(builder): %v", err)
	}
	latestAt := base.Add(time.Minute)
	if err := store.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, latestAt, latestAt, "heartbeat_tick", 37.5, 4096); err != nil {
		t.Fatalf("AppendComputeNodeHeartbeatWithStats(latest): %v", err)
	}
	if err := store.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, latestAt, latestAt, "heartbeat_tick", 99, 99); !errors.Is(err, ErrConflict) {
		t.Fatalf("AppendComputeNodeHeartbeatWithStats(duplicate) = %v, want ErrConflict", err)
	}
	if err := store.AppendComputeNodeHeartbeat(ctx, peer.ID, latestAt, latestAt, "heartbeat_tick"); err != nil {
		t.Fatalf("AppendComputeNodeHeartbeat(peer): %v", err)
	}

	all, err := store.LatestHeartbeatStats(ctx)
	if err != nil {
		t.Fatalf("LatestHeartbeatStats: %v", err)
	}
	byNode := make(map[string]ComputeNodeHeartbeatStats, len(all))
	for _, row := range all {
		byNode[row.NodeID] = row
	}
	got, ok := byNode[node.ID]
	if !ok {
		t.Fatalf("LatestHeartbeatStats omitted node %s: %+v", node.ID, all)
	}
	if !got.ReceivedAt.Equal(latestAt) || got.CPUPct60s == nil || *got.CPUPct60s != 37.5 || got.DiskUsedBytes == nil || *got.DiskUsedBytes != 4096 {
		t.Errorf("latest node stats = %+v, want heartbeat_tick sample", got)
	}
	if gotPeer, ok := byNode[peer.ID]; !ok || gotPeer.CPUPct60s != nil || gotPeer.DiskUsedBytes != nil {
		t.Errorf("pre-stats peer projection = %+v, present=%v; want nil stats", gotPeer, ok)
	}

	builder, err := store.LatestBuilderHeartbeatStats(ctx)
	if err != nil {
		t.Fatalf("LatestBuilderHeartbeatStats: %v", err)
	}
	if len(builder) != 1 {
		t.Fatalf("LatestBuilderHeartbeatStats len = %d, want 1: %+v", len(builder), builder)
	}
	if got := builder[0]; got.NodeID != node.ID || !got.ReceivedAt.Equal(base) || got.CPUPct60s == nil || *got.CPUPct60s != 12.5 {
		t.Errorf("builder stats = %+v, want older builder-specific sample", got)
	}
}

func TestMemStoreListComputeNodesPageHonorsLimit(t *testing.T) {
	ctx := context.Background()
	store := NewMemStore()
	for _, name := range []string{"page-zulu", "page-alpha"} {
		if _, err := store.CreateComputeNode(ctx, ComputeNode{Name: name, TargetURL: "unix:///run/faas/" + name + ".sock", Active: true}); err != nil {
			t.Fatalf("CreateComputeNode(%s): %v", name, err)
		}
	}

	empty, err := store.ListComputeNodesPage(ctx, true, 0)
	if err != nil || len(empty) != 0 {
		t.Fatalf("ListComputeNodesPage(limit=0) = (%+v, %v), want empty", empty, err)
	}
	page, err := store.ListComputeNodesPage(ctx, true, 1)
	if err != nil {
		t.Fatalf("ListComputeNodesPage(limit=1): %v", err)
	}
	if len(page) != 1 {
		t.Fatalf("ListComputeNodesPage(limit=1) len = %d, want 1", len(page))
	}
}
