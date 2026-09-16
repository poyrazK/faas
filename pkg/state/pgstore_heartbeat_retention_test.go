package state_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

func TestPg_MaintainComputeNodeHeartbeatHistoryRollsUpTenNodeFleet(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	now := time.Now().UTC().Truncate(time.Second)
	const nodeCount = 11
	for i := 0; i < nodeCount; i++ {
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: fmt.Sprintf("heartbeat-retention-%02d", i), TargetURL: fmt.Sprintf("unix:///run/faas/vmmd-%02d.sock", i),
			Active: true, VPCPUs: 4, VCPUBudget: 4, MemMB: 8192, MaxConcurrency: 16, AdmissionCeilingMB: 4096,
		})
		if err != nil {
			t.Fatalf("CreateComputeNode(%d): %v", i, err)
		}
		for sample := 0; sample < 3; sample++ {
			at := now.Add(-8*24*time.Hour + time.Duration(sample)*time.Minute)
			if err := store.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, at, at, "heartbeat_tick", float64(10+i+sample), int64(1000+i+sample)); err != nil {
				t.Fatalf("append old heartbeat node=%d sample=%d: %v", i, sample, err)
			}
		}
		if err := store.AppendComputeNodeHeartbeatWithStats(ctx, node.ID, now.Add(-time.Hour), now.Add(-time.Hour), "heartbeat_tick", 20, 2000); err != nil {
			t.Fatalf("append recent heartbeat node=%d: %v", i, err)
		}
	}

	var deleted int64
	for {
		result, err := store.MaintainComputeNodeHeartbeatHistory(ctx, now.Add(-7*24*time.Hour), 13)
		if err != nil {
			t.Fatal(err)
		}
		deleted += result.Deleted
		if result.Deleted < 13 {
			break
		}
	}
	if deleted != nodeCount*3 {
		t.Fatalf("deleted=%d, want %d", deleted, nodeCount*3)
	}

	var rawRows, rolledSamples int
	if err := pool.QueryRow(ctx, `select count(*) from compute_node_heartbeats where received_at < $1`, now.Add(-7*24*time.Hour)).Scan(&rawRows); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select coalesce(sum(sample_count), 0) from compute_node_heartbeat_hourly`).Scan(&rolledSamples); err != nil {
		t.Fatal(err)
	}
	if rawRows != 0 || rolledSamples != nodeCount*3 {
		t.Fatalf("raw=%d rolled_samples=%d, want 0/%d", rawRows, rolledSamples, nodeCount*3)
	}

	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	stats, err := store.LatestHeartbeatStats(readCtx)
	if err != nil {
		t.Fatalf("LatestHeartbeatStats after retention: %v", err)
	}
	if len(stats) < nodeCount {
		t.Fatalf("latest stats nodes=%d, want at least %d", len(stats), nodeCount)
	}
}
