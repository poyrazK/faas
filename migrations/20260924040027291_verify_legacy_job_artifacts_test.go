//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestVerifyLegacyJobArtifactsMigration replays the new migration against
// realistic pre-fix rows. A fresh-schema migration alone cannot prove the
// data repair selects only falsely-ready legacy artifact references.
func TestVerifyLegacyJobArtifactsMigration(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	accountID := seedAccount(t, ctx, pool)
	seed := func(name, ref, key string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			insert into jobs (account_id, kind, name, image_ref, command,
			  ram_mb, task_timeout_s, max_parallelism, retry_max,
			  image_materialization_status, image_storage_key)
			values ($1::uuid, 'batch', $2, $3, array['/bin/true'],
			  256, 60, 1, 0, 'ready', $4)
			returning id::text`, accountID, name, ref, key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	legacyID := seed("legacy-found", "apps/legacy-job/artifact.ext4", "apps/legacy-job/artifact.ext4")
	ociID := seed("oci-present", "registry.example/worker:latest", "jobs/worker.ext4")
	badKeyID := seed("legacy-wrong-key", "apps/legacy-job/other.ext4", "jobs/other.ext4")
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20260924040027291`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay legacy verification migration: %v", err)
	}
	for _, tc := range []struct {
		id, want string
	}{
		{id: legacyID, want: "verifying_legacy"},
		{id: ociID, want: "ready"},
		{id: badKeyID, want: "ready"},
	} {
		var status string
		if err := pool.QueryRow(ctx, `select image_materialization_status from jobs where id=$1::uuid`, tc.id).Scan(&status); err != nil {
			t.Fatal(err)
		}
		if status != tc.want {
			t.Errorf("job %s status = %q, want %q", tc.id, status, tc.want)
		}
	}
}
