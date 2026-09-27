//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_ScalingPolicyRevisionTracksRuntimeInputs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID, appID := uuid.NewString(), uuid.NewString()
	slug := "app-scaling-rev-" + appID[:8]
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES ($1, $2, 'pro', now())
	`, accountID, "app-scaling-rev-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, slug); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	readRevision := func() int64 {
		t.Helper()
		var revision int64
		if err := pool.QueryRow(ctx, `SELECT scaling_policy_revision FROM apps WHERE id = $1`, appID).Scan(&revision); err != nil {
			t.Fatalf("read scaling policy revision: %v", err)
		}
		return revision
	}
	if got := readRevision(); got != 1 {
		t.Fatalf("initial revision = %d, want 1", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET scaling_policy = '{"min_instances":1,"max_instances":4}'::jsonb WHERE id = $1`, appID); err != nil {
		t.Fatalf("update scaling policy: %v", err)
	}
	if got := readRevision(); got != 2 {
		t.Fatalf("revision after policy update = %d, want 2", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET max_concurrency = 2 WHERE id = $1`, appID); err != nil {
		t.Fatalf("update scaling cap: %v", err)
	}
	if got := readRevision(); got != 3 {
		t.Fatalf("revision after max concurrency update = %d, want 3", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET workload_class = 'worker' WHERE id = $1`, appID); err != nil {
		t.Fatalf("update workload class: %v", err)
	}
	if got := readRevision(); got != 4 {
		t.Fatalf("revision after workload class update = %d, want 4", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET max_concurrency = 2, ram_mb = 256 WHERE id = $1`, appID); err != nil {
		t.Fatalf("update no-op scaling cap and unrelated RAM: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET scaling_policy_revision = 99 WHERE id = $1`, appID); err != nil {
		t.Fatalf("attempt to forge revision: %v", err)
	}
	if got := readRevision(); got != 4 {
		t.Fatalf("revision after no-op/unrelated/forged writes = %d, want 4", got)
	}
	var statusTable int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'app_scaling_policy_scheduler_status'`).Scan(&statusTable); err != nil {
		t.Fatalf("check scheduler status table: %v", err)
	}
	if statusTable != 1 {
		t.Fatal("app_scaling_policy_scheduler_status table missing")
	}
}
