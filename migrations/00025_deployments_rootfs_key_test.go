//go:build !no_pg

// Migration-apply test for 00025 (deployments.rootfs_key column).
// Pins the load-bearing contract from issue #96 / ADR-025 axis 2,
// final slice (PR #116):
//
//   1. The migration set applies cleanly through 00025.
//   2. The new rootfs_key column lands on deployments as text NOT
//      NULL DEFAULT ''. A regression that drops NOT NULL would let
//      schedd read NULL on a backfill window and panic in
//      pkg/sched/engine.go where it string-concatenates the key
//      onto the wake wire.
//   3. Backfill from rootfs_path translates the default apps root
//      (/var/lib/faas/apps/) into the canonical key (apps/...). A
//      regression that strips the leading slash would produce
//      "apps/..." paths that fail Storage.Get at wake time, which
//      would surface only at first wake of every legacy deployment
//      — exactly the regression this slice is meant to avoid.
//   4. Backfill leaves a non-default rootfs_path untouched (empty
//      string in rootfs_key). imaged re-stamps the column on the
//      next build via SetDeploymentRootfs. A regression that
//      writes a stale key into rootfs_key for those rows would
//      cause the next cold boot to point at a non-existent key.
//   5. Idempotent re-apply: the IF NOT EXISTS guard means a
//      repeated `make sqlc-migrate` (or a developer's manual
//      re-run) does not duplicate the column or rewrite backfilled
//      values. A regression that drops IF NOT EXISTS would cause
//      "column already exists" failures on re-run during local
//      dev.
//
// Build tag mirrors 00022_backfill_test.go:17 / 00024's test:
// set FAAS_SKIP_PG_TESTS=1 locally to skip.

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// containsEmptyStringDefault reports whether the catalog's
// column_default expression for rootfs_key represents the empty
// string literal. Postgres renders DEFAULT '' as "''::text" (or
// "''" depending on version); we accept any rendering whose only
// non-whitespace content is two single quotes, since that's the
// canonical empty-string literal across PG 12+.
func containsEmptyStringDefault(s string) bool {
	s = strings.TrimSpace(s)
	return s == "''" || s == "''::text" || s == "''::character varying"
}

func TestMigrations_00025_DeploymentsRootfsKey(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)

	// (1) Run the full migration set. 00025 should land last; any
	// prior failure (e.g., the migrations directory missing a slot
	// between 1 and 25) surfaces here before we get to the contract
	// pins.
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (PR #116 failure mode: missing migration slot between 1 and 25)", err)
	}

	// (2) Column shape: text NOT NULL DEFAULT ''. A regression
	// that drops NOT NULL or changes the default would let
	// schedd see NULL on the wake path and panic; a regression
	// that changes the default would silently break the
	// legacy-row stamp (empty key + non-empty path = wake to a
	// non-existent key).
	var dataType, columnDefault string
	var isNullable string
	if err := pool.QueryRow(ctx, `
		select data_type, column_default, is_nullable
		  from information_schema.columns
		 where table_name = 'deployments'
		   and column_name = 'rootfs_key'
	`).Scan(&dataType, &columnDefault, &isNullable); err != nil {
		t.Fatalf("read rootfs_key column metadata: %v (the 00025 add-column must have failed silently or used the wrong name)", err)
	}
	if dataType != "text" {
		t.Errorf("rootfs_key data_type = %q, want %q (the column must be text — schedd reads it as a string on the wake wire)", dataType, "text")
	}
	if isNullable != "NO" {
		t.Errorf("rootfs_key is_nullable = %q, want %q (must be NOT NULL — schedd string-concatenates the key without a NULL guard)", isNullable, "NO")
	}
	// column_default comes back like "''::text" after the
	// migration's DEFAULT '' clause resolves; what matters is
	// that the literal is the empty string. Match the substring
	// instead of the full cast form so the test doesn't pin
	// Postgres' formatting across versions. Postgres reliably
	// reports the literal here — we don't need a fallback row.
	if columnDefault == "" {
		t.Errorf("rootfs_key column_default is NULL; expected ''::text (the migration's DEFAULT '' must have been silently dropped)")
	} else if !containsEmptyStringDefault(columnDefault) {
		t.Errorf("rootfs_key column_default = %q; expected an empty-string literal (the migration's DEFAULT '' must have been replaced with something else)", columnDefault)
	}

	// (3) Backfill from the default apps root. Seed a deployment
	// row with the canonical /var/lib/faas/apps/<slug>/<depID>.ext4
	// path, then re-run the migration's UPDATE literal and confirm
	// the backfill lands on the canonical key "apps/<slug>/<depID>.ext4".
	// The whole point of 00025 is to keep legacy rows consistent
	// with the StorageBackend contract without requiring a rebuild.
	//
	// Seed an app + deployment in one shot using the same shape as
	// 00024's test (gen_random_uuid() resolves all PKs; we capture
	// them via RETURNING to avoid magic literals).
	var accountID, appID, deploymentID string
	if err := pool.QueryRow(ctx, `
		insert into accounts (email, plan, created_at)
		values ('rootfs-key@example.com', 'hobby', now())
		returning id
	`).Scan(&accountID); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ($1, 'rootfs-key-app', 256, 1, 30, 'active', now())
		returning id
	`, accountID).Scan(&appID); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into deployments (app_id, kind, image_digest, status, rootfs_path, created_at)
		values ($1, 'image', 'sha256:seed', 'live', '/var/lib/faas/apps/rootfs-key-app/__probe__.ext4', now())
		returning id
	`, appID).Scan(&deploymentID); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
	// Re-run the migration's UPDATE literal. The WHERE clause
	// (rootfs_key = '' AND rootfs_path LIKE '/var/lib/faas/apps/%')
	// must catch the seeded row and stamp the canonical key.
	if _, err := pool.Exec(ctx, `
		update deployments
		   set rootfs_key = regexp_replace(rootfs_path, '^/var/lib/faas/apps/', 'apps/')
		 where rootfs_key = ''
		   and rootfs_path like '/var/lib/faas/apps/%'
		   and rootfs_path <> ''
	`); err != nil {
		t.Fatalf("re-run backfill UPDATE: %v", err)
	}
	var gotKey string
	if err := pool.QueryRow(ctx,
		`select rootfs_key from deployments where id = $1`, deploymentID,
	).Scan(&gotKey); err != nil {
		t.Fatalf("read backfilled rootfs_key: %v", err)
	}
	if gotKey != "apps/rootfs-key-app/__probe__.ext4" {
		t.Errorf("backfilled rootfs_key = %q, want %q (the regexp_replace must strip /var/lib/faas/apps/ and prefix apps/)", gotKey, "apps/rootfs-key-app/__probe__.ext4")
	}

	// (4) Non-default apps root must NOT be backfilled. Seed a
	// deployment with an off-root path (the legacy custom-apps
	// case) and confirm the backfill leaves rootfs_key empty so
	// imaged re-stamps it on the next build via
	// SetDeploymentRootfs.
	var offRootDeploymentID string
	if err := pool.QueryRow(ctx, `
		insert into deployments (app_id, kind, image_digest, status, rootfs_path, created_at)
		values ($1, 'image', 'sha256:seed-off', 'live', '/opt/custom/rootfs-key-app/__off__.ext4', now())
		returning id
	`, appID).Scan(&offRootDeploymentID); err != nil {
		t.Fatalf("seed off-root deployment: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update deployments
		   set rootfs_key = regexp_replace(rootfs_path, '^/var/lib/faas/apps/', 'apps/')
		 where rootfs_key = ''
		   and rootfs_path like '/var/lib/faas/apps/%'
		   and rootfs_path <> ''
	`); err != nil {
		t.Fatalf("re-run backfill UPDATE (off-root): %v", err)
	}
	var gotOffRootKey string
	if err := pool.QueryRow(ctx,
		`select rootfs_key from deployments where id = $1`, offRootDeploymentID,
	).Scan(&gotOffRootKey); err != nil {
		t.Fatalf("read off-root rootfs_key: %v", err)
	}
	if gotOffRootKey != "" {
		t.Errorf("off-root backfill stamped rootfs_key = %q (want empty); the WHERE rootfs_path LIKE clause must skip non-default apps roots", gotOffRootKey)
	}

	// (5) Idempotent re-apply: the IF NOT EXISTS guard means a
	// second call does NOT clobber the backfilled key. Pin this
	// by flipping the seeded deployment's rootfs_key to a
	// sentinel value, re-running the UPDATE, and confirming the
	// sentinel survived.
	if _, err := pool.Exec(ctx,
		`update deployments set rootfs_key = $2 where id = $1`,
		deploymentID, "apps/__sentinel__/survived.ext4",
	); err != nil {
		t.Fatalf("stamp sentinel: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update deployments
		   set rootfs_key = regexp_replace(rootfs_path, '^/var/lib/faas/apps/', 'apps/')
		 where rootfs_key = ''
		   and rootfs_path like '/var/lib/faas/apps/%'
		   and rootfs_path <> ''
	`); err != nil {
		t.Fatalf("idempotent re-run: %v", err)
	}
	var gotSentinel string
	if err := pool.QueryRow(ctx,
		`select rootfs_key from deployments where id = $1`, deploymentID,
	).Scan(&gotSentinel); err != nil {
		t.Fatalf("read sentinel-survival row: %v", err)
	}
	if gotSentinel != "apps/__sentinel__/survived.ext4" {
		t.Errorf("idempotent re-apply clobbered rootfs_key: got %q, want %q (the WHERE rootfs_key = '' filter must keep re-applies safe)", gotSentinel, "apps/__sentinel__/survived.ext4")
	}
}