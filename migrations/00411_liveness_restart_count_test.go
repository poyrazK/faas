// migrations/00411_liveness_restart_count_test.go — pins the
// shape of the liveness_restart_count column on deployments
// (issue #586 / ADR-129 / cluster C commit 12 of the
// platform-observability mega-PR).

//go:build pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func Test_00411_LivenessRestartCount(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	t.Run("default is 0", func(t *testing.T) {
		// Pre-existing deployments rows from older migrations
		// must surface liveness_restart_count=0 via the column
		// default. Insert a fresh row and read back; the DEFAULT
		// path must be exactly 0 (not NULL — the column is
		// NOT NULL).
		var got int
		err := pool.QueryRow(ctx,
			`INSERT INTO deployments (id, app_id, source_path)
			 VALUES ('00000000-0000-0000-0000-000000000411',
			         '00000000-0000-0000-0000-000000000412',
			         '/tmp/test-default')
			 RETURNING liveness_restart_count`).Scan(&got)
		if err != nil {
			t.Fatalf("insert with default liveness_restart_count: %v", err)
		}
		if got != 0 {
			t.Errorf("liveness_restart_count default: got %d, want 0", got)
		}
	})

	t.Run("update bumps value", func(t *testing.T) {
		// Bump the column and read back. pkg/state/pgstore.go's
		// RecordRestart issues this exact UPDATE pattern.
		_, err := pool.Exec(ctx,
			`UPDATE deployments
			 SET liveness_restart_count = liveness_restart_count + 1
			 WHERE id = '00000000-0000-0000-0000-000000000411'`)
		if err != nil {
			t.Fatalf("bump liveness_restart_count: %v", err)
		}
		var got int
		err = pool.QueryRow(ctx,
			`SELECT liveness_restart_count FROM deployments
			 WHERE id = '00000000-0000-0000-0000-000000000411'`).Scan(&got)
		if err != nil {
			t.Fatalf("read liveness_restart_count: %v", err)
		}
		if got != 1 {
			t.Errorf("liveness_restart_count after bump: got %d, want 1", got)
		}
	})

	t.Run("check rejects negative", func(t *testing.T) {
		// The CHECK constraint must reject a direct negative
		// UPDATE — the column is monotonic in the application
		// code (pkg/state/pgstore.RecordRestart never decrements)
		// so the SQL-level guard catches bugs at the boundary.
		_, err := pool.Exec(ctx,
			`UPDATE deployments
			 SET liveness_restart_count = -1
			 WHERE id = '00000000-0000-0000-0000-000000000411'`)
		if err == nil {
			t.Fatal("negative liveness_restart_count accepted; MUST be rejected by CHECK")
		}
		if !strings.Contains(err.Error(), "23514") {
			t.Errorf("expected 23514 check_violation; got %v", err)
		}
	})

	t.Run("down removes column", func(t *testing.T) {
		// MigrateDown round-trip — required by every migration
		// per the migration-test convention. Mirror 00410
		// (app_secret_value_hash_test.go) and 00264
		// (deployments_secret_findings).
		if err := db.MigrateDown(ctx, pool); err != nil {
			t.Fatalf("migrate-down: %v", err)
		}
		var n int
		err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.columns
			 WHERE table_name = 'deployments'
			   AND column_name = 'liveness_restart_count'`).Scan(&n)
		if err != nil {
			t.Fatalf("column-existence check: %v", err)
		}
		if n != 0 {
			t.Errorf("liveness_restart_count column still present after MigrateDown; got %d rows, want 0", n)
		}
		// Re-apply so the post-test fixture is restored.
		if err := db.MigrateUp(ctx, pool); err != nil {
			t.Fatalf("re-migrate-up: %v", err)
		}
	})
}
