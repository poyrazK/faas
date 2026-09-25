//go:build !no_pg

package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgGatewayControlPlaneWatermarkResetsAcrossBoots(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	role, gatewayURL := "compute-only", "tcp://127.0.0.1:9090"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "policy-watermark-" + uuid.NewString()[:8], TargetURL: "unix:///run/vmmd.sock",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL,
	})
	if err != nil {
		t.Fatalf("create serving node: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteComputeNode(ctx, node.ID) })
	find := func() state.ServingGatewayControlPlaneState {
		t.Helper()
		rows, err := store.ListServingGatewayControlPlaneStates(ctx)
		if err != nil {
			t.Fatalf("list gateway states: %v", err)
		}
		for _, row := range rows {
			if row.NodeName == node.Name {
				return row
			}
		}
		t.Fatalf("serving node %s missing from policy status", node.Name)
		return state.ServingGatewayControlPlaneState{}
	}
	if got := find(); got.LastChangeID != 0 {
		t.Fatalf("unobserved node revision = %d, want 0", got.LastChangeID)
	}
	bootA, bootB := uuid.NewString(), uuid.NewString()
	for _, revision := range []int64{5, 4} {
		if err := store.UpsertGatewayControlPlaneWatermark(ctx, node.Name, bootA, revision); err != nil {
			t.Fatalf("upsert boot A revision %d: %v", revision, err)
		}
	}
	if got := find(); got.LastChangeID != 5 {
		t.Fatalf("same-boot revision = %d, want monotonic 5", got.LastChangeID)
	}
	if err := store.UpsertGatewayControlPlaneWatermark(ctx, node.Name, bootB, 2); err != nil {
		t.Fatalf("upsert boot B: %v", err)
	}
	if got := find(); got.LastChangeID != 2 {
		t.Fatalf("new-boot revision = %d, want reset to 2", got.LastChangeID)
	}
}

func TestPgPruneControlPlaneChangeLogWaitsForServingGateways(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	role := "compute-only"
	createNode := func(port string) state.ComputeNode {
		t.Helper()
		gatewayURL := "tcp://127.0.0.1:" + port
		node, err := store.CreateComputeNode(ctx, state.ComputeNode{
			Name: "policy-prune-" + uuid.NewString()[:8], TargetURL: "unix:///run/vmmd-" + port + ".sock",
			VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
			VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL,
		})
		if err != nil {
			t.Fatalf("create serving node: %v", err)
		}
		t.Cleanup(func() { _ = store.DeleteComputeNode(ctx, node.ID) })
		return node
	}
	first, second := createNode("9090"), createNode("9091")
	appID := uuid.NewString()
	var ids [3]int64
	for i := range ids {
		if err := pool.QueryRow(ctx, `
			INSERT INTO control_plane_change_log (resource_type, resource_id, app_id, operation, created_at)
			VALUES ('app', $1, $1, 'updated', now() - interval '40 days')
			RETURNING id
		`, appID).Scan(&ids[i]); err != nil {
			t.Fatalf("seed ledger change %d: %v", i, err)
		}
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if removed, err := store.PruneControlPlaneChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("prune before observations = %d, %v; want 0, nil", removed, err)
	}
	bootFirst, bootSecond := uuid.NewString(), uuid.NewString()
	if err := store.UpsertGatewayControlPlaneWatermark(ctx, first.Name, bootFirst, ids[0]); err != nil {
		t.Fatalf("first gateway watermark: %v", err)
	}
	if err := store.UpsertGatewayControlPlaneWatermark(ctx, second.Name, bootSecond, ids[2]); err != nil {
		t.Fatalf("second gateway watermark: %v", err)
	}
	if removed, err := store.PruneControlPlaneChangeLog(ctx, cutoff); err != nil || removed != 1 {
		t.Fatalf("prune with lagging gateway = %d, %v; want 1, nil", removed, err)
	}
	if err := store.UpsertGatewayControlPlaneWatermark(ctx, first.Name, bootFirst, ids[2]); err != nil {
		t.Fatalf("advance first gateway watermark: %v", err)
	}
	if removed, err := store.PruneControlPlaneChangeLog(ctx, cutoff); err != nil || removed != 2 {
		t.Fatalf("prune after all gateways catch up = %d, %v; want 2, nil", removed, err)
	}
}

func TestPgEdgeRuleWatermarkTracksBootsAndProtectsReplayHistory(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	role := "compute-only"
	gatewayURL := "tcp://127.0.0.1:9190"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "edge-policy-watermark-" + uuid.NewString()[:8], TargetURL: "unix:///run/vmmd-edge-policy.sock",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL,
	})
	if err != nil {
		t.Fatalf("create serving node: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteComputeNode(ctx, node.ID) })
	appID, ruleID := uuid.NewString(), uuid.NewString()
	var changeID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO edge_rule_change_log (app_id, rule_id, operation, created_at)
		VALUES ($1, $2, 'created', now() - interval '40 days')
		RETURNING id
	`, appID, ruleID).Scan(&changeID); err != nil {
		t.Fatalf("seed edge-rule ledger: %v", err)
	}
	if got, err := store.LatestAppEdgeRuleChangeID(ctx, appID); err != nil || got != changeID {
		t.Fatalf("latest app edge-rule revision = %d, %v; want %d, nil", got, err, changeID)
	}
	find := func() state.ServingGatewayControlPlaneState {
		t.Helper()
		rows, err := store.ListServingGatewayControlPlaneStates(ctx)
		if err != nil {
			t.Fatalf("list gateway states: %v", err)
		}
		for _, row := range rows {
			if row.NodeName == node.Name {
				return row
			}
		}
		t.Fatalf("serving node %s missing from policy status", node.Name)
		return state.ServingGatewayControlPlaneState{}
	}
	if got := find(); got.LastEdgeRuleChangeID != 0 || !got.EdgeRulesObservedAt.Before(time.Now().UTC().Add(-10*time.Second)) {
		t.Fatalf("unobserved edge-rule state = %+v, want zero revision and no observation", got)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if removed, err := store.PruneEdgeRuleChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("prune before edge observation = %d, %v; want 0, nil", removed, err)
	}
	bootA, bootB := uuid.NewString(), uuid.NewString()
	for _, revision := range []int64{changeID, changeID - 1} {
		if err := store.UpsertGatewayEdgeRuleWatermark(ctx, node.Name, bootA, revision); err != nil {
			t.Fatalf("upsert boot A revision %d: %v", revision, err)
		}
	}
	if got := find(); got.LastEdgeRuleChangeID != changeID || got.EdgeRulesObservedAt.IsZero() {
		t.Fatalf("same-boot edge-rule state = %+v, want revision %d and observation", got, changeID)
	}
	if err := store.UpsertGatewayEdgeRuleWatermark(ctx, node.Name, bootB, 0); err != nil {
		t.Fatalf("upsert new boot watermark: %v", err)
	}
	if got := find(); got.LastEdgeRuleChangeID != 0 {
		t.Fatalf("new-boot edge-rule revision = %d, want reset to 0", got.LastEdgeRuleChangeID)
	}
	if removed, err := store.PruneEdgeRuleChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("prune after boot reset = %d, %v; want 0, nil", removed, err)
	}
	if err := store.UpsertGatewayEdgeRuleWatermark(ctx, node.Name, bootB, changeID); err != nil {
		t.Fatalf("advance new boot watermark: %v", err)
	}
	if removed, err := store.PruneEdgeRuleChangeLog(ctx, cutoff); err != nil || removed != 1 {
		t.Fatalf("prune after gateway caught up = %d, %v; want 1, nil", removed, err)
	}
}
