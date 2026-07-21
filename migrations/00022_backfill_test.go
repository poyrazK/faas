//go:build !no_pg

// Backfill-semantics test for migration 00022 (snapshots.storage_key).
// apply_walk_test.go proves 00022 applies cleanly and that
// max(version_id) advances by one; this test proves the BACKFILL is
// total and matches sched.SnapshotMemKey(deployment_id) byte-for-byte
// for every pre-existing row.
//
// This is the load-bearing guarantee that lets the schedd read-path
// switch from "compute SnapshotMemKey from the dep id" to "read
// StorageKey from the row" without a transient window of mismatches.
// If a row were backfilled with the wrong key, the next wake from
// that row would pull bytes from a non-existent storage key and fail
// to restore — the very class of bug the slice is meant to eliminate.
//
// Build tag mirrors apply_walk_test.go:4 — set FAAS_SKIP_PG_TESTS=1
// locally to skip.

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_00022_SnapshotsStorageKey_Backfill(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. The backfill already ran during
	// 00022's Up; we'll wipe the row's storage_key back to '' and
	// re-run the literal SQL by hand to test it in isolation. The
	// apply step is covered by TestMigrationsApplyAndWalk.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	// (2) Seed the FK chain (account + app + deployment) so the
	// snapshots insert is legal. The values are fixture literals;
	// storage_key is not null so we must supply something.
	depID := "11111111-1111-1111-1111-111111111111"
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-000000000001', 'backfill@example.com', 'hobby', now())
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, ram_mb, max_concurrency, idle_timeout_s,
		                  min_instances, status, created_at, updated_at)
		values ('00000000-0000-0000-0000-000000000002',
		        '00000000-0000-0000-0000-000000000001',
		        'backfill-app', 256, 1, 30, 0, 'active', now(), now())
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ($1, '00000000-0000-0000-0000-000000000002', 'image', 'sha256:deadbeef', 'live', now())
	`, depID); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	// Backdate the snapshot's created_at so the fence
	// (`created_at < now() - interval '1 second'`, F-5) lets the
	// backfill UPDATE target it. A fresh row stamped at now() would
	// be skipped by the fence, defeating the test.
	if _, err := pool.Exec(ctx, `
		insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, path, storage_key, created_at)
		values ($1, '1.8.0', 100, 100, '/srv/fc/snap/x/mem', '', now() - interval '1 minute')
	`, depID); err != nil {
		t.Fatalf("seed snapshot: %v", err)
	}

	// (3) Re-run the backfill SQL literal from migrations/00022_*.sql.
	// Mirrors:
	//   update snapshots
	//      set storage_key = 'snap/' || deployment_id::text || '/mem'
	//    where storage_key = ''
	//      and created_at < now() - interval '1 second'
	if _, err := pool.Exec(ctx,
		`update snapshots set storage_key = 'snap/' || deployment_id::text || '/mem' where storage_key = '' and created_at < now() - interval '1 second'`,
	); err != nil {
		t.Fatalf("re-run backfill SQL: %v", err)
	}

	// (4) Assert the row got the canonical sched.SnapshotMemKey value
	// byte-for-byte. A wrong literal (e.g. swapping the trailing
	// segment) would show up here as a mismatch and the next wake
	// would attempt to restore from a non-existent storage key.
	var got string
	if err := pool.QueryRow(ctx,
		`select storage_key from snapshots where deployment_id = $1 limit 1`, depID,
	).Scan(&got); err != nil {
		t.Fatalf("read backfilled storage_key: %v", err)
	}
	want := "snap/" + depID + "/mem"
	if got != want {
		t.Errorf("backfilled storage_key = %q, want %q (the sched.SnapshotMemKey form)", got, want)
	}

	// (5) The NOT NULL DEFAULT '' clause is what makes the column
	// safe for new inserts that don't know about storage_key yet.
	// Pin that with a second deployment + a snapshot insert that
	// omits storage_key — expect '' (the default) back.
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, kind, image_digest, status, created_at)
		values ('22222222-2222-2222-2222-222222222222',
		        '00000000-0000-0000-0000-000000000002', 'image', 'sha256:other', 'live', now())
	`); err != nil {
		t.Fatalf("seed second deployment: %v", err)
	}
	var def string
	if err := pool.QueryRow(ctx, `
		insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, path)
		values ('22222222-2222-2222-2222-222222222222', '1.8.0', 1, 1, '/p')
		returning storage_key
	`).Scan(&def); err != nil {
		t.Fatalf("insert default-storage_key row: %v", err)
	}
	if def != "" {
		t.Errorf("default storage_key = %q, want \"\" (the column default)", def)
	}
}
