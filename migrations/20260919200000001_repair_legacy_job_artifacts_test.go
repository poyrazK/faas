//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

const repairLegacyJobArtifactsVersion int64 = 20260919200000001

func TestMigrationRepairLegacyJobArtifacts(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	migrateUpOnce(ctx, t, pool)
	accountID := seedAccount(t, ctx, pool)

	legacyKey := "apps/legacy-job/00000000-0000-4000-8000-000000000123.ext4"
	var legacyID, ociID string
	if err := pool.QueryRow(ctx, `
		insert into jobs (account_id, kind, name, image_ref, command, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max)
		values ($1, 'batch', 'legacy-artifact', $2, array['/bin/true'], 256, 60, 1, 0)
		returning id`, accountID, legacyKey).Scan(&legacyID); err != nil {
		t.Fatalf("seed legacy job: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		insert into jobs (account_id, kind, name, image_ref, command, ram_mb,
		                  task_timeout_s, max_parallelism, retry_max)
		values ($1, 'batch', 'oci-reference', 'docker.io/library/alpine:3.20', array['/bin/true'], 256, 60, 1, 0)
		returning id`, accountID).Scan(&ociID); err != nil {
		t.Fatalf("seed OCI job: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		update jobs set
			image_materialization_attempts = 2,
			image_materialization_lease_owner = 'stale-worker',
			image_materialization_lease_until = now() + interval '15 minutes'
		where id = $1`, legacyID); err != nil {
		t.Fatalf("seed stale lease: %v", err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id = $1`, repairLegacyJobArtifactsVersion); err != nil {
		t.Fatalf("remove repair migration ledger row: %v", err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("reapply repair migration: %v", err)
	}

	var status, storageKey string
	var digest, leaseOwner *string
	var attempts int
	if err := pool.QueryRow(ctx, `
		select image_materialization_status, coalesce(image_storage_key, ''),
		       image_resolved_digest, image_materialization_attempts,
		       image_materialization_lease_owner
		  from jobs where id = $1`, legacyID).Scan(&status, &storageKey, &digest, &attempts, &leaseOwner); err != nil {
		t.Fatal(err)
	}
	if status != "ready" || storageKey != legacyKey || digest != nil || attempts != 0 || leaseOwner != nil {
		t.Fatalf("legacy repair = status=%q key=%q digest=%v attempts=%d owner=%v", status, storageKey, digest, attempts, leaseOwner)
	}
	if err := pool.QueryRow(ctx, `select image_materialization_status, coalesce(image_storage_key, '') from jobs where id = $1`, ociID).Scan(&status, &storageKey); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || storageKey != "" {
		t.Fatalf("OCI row was incorrectly repaired: status=%q key=%q", status, storageKey)
	}
}
