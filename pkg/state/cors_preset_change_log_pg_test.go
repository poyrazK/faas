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

func TestPgCorsPresetWatermarkTracksBootsAndProtectsReplayHistory(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	role, gatewayURL := "compute-only", "tcp://127.0.0.1:9290"
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{
		Name: "cors-policy-" + uuid.NewString()[:8], TargetURL: "unix:///run/vmmd-cors-policy.sock",
		VPCPUs: 1, MemMB: 1024, MaxConcurrency: 1, AdmissionCeilingMB: 512,
		VCPUBudget: 1, Active: true, Role: &role, GatewayTargetURL: &gatewayURL,
	})
	if err != nil {
		t.Fatalf("create serving node: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteComputeNode(ctx, node.ID) })

	accountA, accountB := uuid.NewString(), uuid.NewString()
	for i, accountID := range []string{accountA, accountB} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO accounts (id, email, plan, created_at)
			VALUES ($1, $2, 'pro', now())
		`, accountID, "cors-status-"+accountID+"@example.com"); err != nil {
			t.Fatalf("seed account %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM cors_preset_change_log WHERE account_id = ANY($1::uuid[])`, []string{accountA, accountB})
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = ANY($1::uuid[])`, []string{accountA, accountB})
	})
	var ids [3]int64
	for i, accountID := range []string{accountA, accountA, accountB} {
		if err := pool.QueryRow(ctx, `
			INSERT INTO cors_preset_change_log (account_id, preset_id, operation, created_at)
			VALUES ($1, $2, 'updated', now() - interval '40 days')
			RETURNING id
		`, accountID, uuid.NewString()).Scan(&ids[i]); err != nil {
			t.Fatalf("seed CORS preset change %d: %v", i, err)
		}
	}
	if got, err := store.LatestAccountCorsPresetChangeID(ctx, accountA); err != nil || got != ids[1] {
		t.Fatalf("latest account A revision = %d, %v; want %d", got, err, ids[1])
	}
	if got, err := store.LatestAccountCorsPresetChangeID(ctx, accountB); err != nil || got != ids[2] {
		t.Fatalf("latest account B revision = %d, %v; want %d", got, err, ids[2])
	}
	changes, err := store.ListCorsPresetChangesAfter(ctx, ids[0], 10)
	if err != nil || len(changes) != 2 || changes[0].ID != ids[1] || changes[0].AccountID != accountA || changes[1].ID != ids[2] || changes[1].AccountID != accountB {
		t.Fatalf("changes after first revision = %+v, %v; want account A then B", changes, err)
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
	if got := find(); got.LastCorsPresetChangeID != 0 || !got.CorsPresetsObservedAt.Before(time.Now().UTC().Add(-10*time.Second)) {
		t.Fatalf("unobserved CORS preset state = %+v, want zero revision and no observation", got)
	}
	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	if removed, err := store.PruneCorsPresetChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("prune before CORS observation = %d, %v; want 0, nil", removed, err)
	}
	bootA, bootB := uuid.NewString(), uuid.NewString()
	if err := reportPolicyTestProgress(t, ctx, store, state.GatewayPolicyCorsPresets, node.Name, bootA, ids[0]); err != nil {
		t.Fatalf("upsert first CORS watermark: %v", err)
	}
	if got := find(); got.LastCorsPresetChangeID != ids[0] || got.CorsPresetsObservedAt.IsZero() {
		t.Fatalf("observed CORS state = %+v; want revision %d", got, ids[0])
	}
	if removed, err := store.PruneCorsPresetChangeLog(ctx, cutoff); err != nil || removed != 1 {
		t.Fatalf("prune with lagging CORS watermark = %d, %v; want first history row only", removed, err)
	}
	if got, err := store.LatestAccountCorsPresetChangeID(ctx, accountA); err != nil || got != ids[1] {
		t.Fatalf("account desired revision after pruning = %d, %v; want retained latest %d", got, err, ids[1])
	}
	if err := reportPolicyTestProgress(t, ctx, store, state.GatewayPolicyCorsPresets, node.Name, bootB, ids[2]); err != nil {
		t.Fatalf("upsert new-boot CORS watermark: %v", err)
	}
	if got := find(); got.LastCorsPresetChangeID != ids[2] {
		t.Fatalf("new boot CORS revision = %d, want reset/advance to %d", got.LastCorsPresetChangeID, ids[2])
	}
	if removed, err := store.PruneCorsPresetChangeLog(ctx, cutoff); err != nil || removed != 0 {
		t.Fatalf("prune latest account revisions = %d, %v; want retain one per account", removed, err)
	}
}
