//go:build !no_pg

// ADR-594: legacy job claim wiring reads a real isolated PostgreSQL identity
// and refuses inference once that database contains a shared compute fleet.
package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestLegacyJobClaimStore_Postgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	local, err := store.ComputeNodeByName(t.Context(), state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	deps := defaultDeps()
	// ConnString retains the original DSN rather than pgtest's isolated
	// search_path/database. Keep each fresh connection in the private fixture.
	deps.openDB = func(ctx context.Context, _ string) (*pgxpool.Pool, error) {
		return pgxpool.NewWithConfig(ctx, pool.Config())
	}
	claimPool, _, id, err := loadLegacyJobClaimStore(t.Context(), deps, "isolated-pgtest")
	if err != nil {
		t.Fatal(err)
	}
	claimPool.Close()
	if id != local.ID {
		t.Fatalf("legacy identity=%q want=%q", id, local.ID)
	}
	if _, err := store.CreateComputeNode(t.Context(), state.ComputeNode{
		Name: "job-peer.faas", TargetURL: "unix:///job-peer", Active: true,
		VPCPUs: 2, MemMB: 1024, MaxConcurrency: 10, AdmissionCeilingMB: 1024, VCPUBudget: 8,
	}); err != nil {
		t.Fatal(err)
	}
	claimPool, _, id, err = loadLegacyJobClaimStore(t.Context(), deps, "isolated-pgtest")
	if err == nil || claimPool != nil || id != "" {
		t.Fatalf("inferred shared-fleet identity: id=%q error=%v", id, err)
	}
}
