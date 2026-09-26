//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_DeploymentTrafficChangesAreDurable(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	accountID := uuid.NewString()
	appID := uuid.NewString()
	var deploymentID string
	t.Cleanup(func() {
		if deploymentID != "" {
			_, _ = pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, deploymentID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM apps WHERE id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM control_plane_change_log WHERE app_id = $1`, appID)
		_, _ = pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, accountID)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, email, plan, created_at)
		VALUES ($1, $2, 'pro', now())
	`, accountID, "traffic-ledger-"+accountID+"@example.com"); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO apps (id, account_id, slug, type, ram_mb, max_concurrency, status, created_at)
		VALUES ($1, $2, $3, 'app', 128, 1, 'active', now())
	`, appID, accountID, "traffic-ledger-"+appID[:8]); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	// The scheduler stamps these columns frequently; they neither change
	// gateway policy nor need to contend for the global ledger-order lock.
	if _, err := pool.Exec(ctx, `
		UPDATE apps SET last_scale_out_at = now(), last_scale_in_at = now()
		WHERE id = $1
	`, appID); err != nil {
		t.Fatalf("stamp scaling bookkeeping: %v", err)
	}
	var appUpdates int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type = 'app' AND operation = 'updated'
	`, appID).Scan(&appUpdates); err != nil {
		t.Fatalf("count bookkeeping app changes: %v", err)
	}
	if appUpdates != 0 {
		t.Fatalf("bookkeeping app changes = %d, want 0", appUpdates)
	}
	if _, err := pool.Exec(ctx, `UPDATE apps SET max_concurrency = 2 WHERE id = $1`, appID); err != nil {
		t.Fatalf("update app policy: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type = 'app' AND operation = 'updated'
	`, appID).Scan(&appUpdates); err != nil {
		t.Fatalf("count app policy changes: %v", err)
	}
	if appUpdates != 1 {
		t.Fatalf("app policy changes = %d, want 1", appUpdates)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO deployments (app_id, image_digest, status, traffic_percent)
		VALUES ($1, $2, 'building', 100) RETURNING id
	`, appID, "sha256:"+strings.Repeat("a", 64)).Scan(&deploymentID); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	for _, percent := range []int{100, 25, 25} {
		if _, err := pool.Exec(ctx, `UPDATE deployments SET traffic_percent = $2 WHERE id = $1`, deploymentID, percent); err != nil {
			t.Fatalf("set traffic to %d: %v", percent, err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type = 'deployment_traffic'
		  AND resource_id = $2 AND operation = 'updated'
	`, appID, deploymentID).Scan(&count); err != nil {
		t.Fatalf("count traffic changes: %v", err)
	}
	if count != 1 {
		t.Fatalf("traffic ledger rows = %d, want exactly one actual change", count)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin rollback check: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE deployments SET traffic_percent = 50 WHERE id = $1`, deploymentID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("update in rollback check: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback traffic update: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM control_plane_change_log
		WHERE app_id = $1 AND resource_type = 'deployment_traffic'
	`, appID).Scan(&count); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if count != 1 {
		t.Fatalf("traffic ledger rows after rollback = %d, want 1", count)
	}
}
