package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 397
func TestObjectWriteHistoryRecoveryMigrations(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE object_upload_completions(id int PRIMARY KEY,bucket_id int);
CREATE TABLE object_storage_capacity_reconciliations(bucket_id int,state text,last_error_code text DEFAULT '',CONSTRAINT object_storage_capacity_reconciliations_last_error_code_check CHECK(last_error_code IN ('','untracked_writes','unsettled_writes','multipart_active','deadline','inventory_failed')));
CREATE TABLE object_storage_bucket_usage(bucket_id int PRIMARY KEY,baseline_bytes bigint DEFAULT 0,baseline_keys bigint DEFAULT 0,granted_bytes bigint DEFAULT 0,granted_keys bigint DEFAULT 0);
INSERT INTO object_upload_completions VALUES(1,1);
INSERT INTO object_storage_capacity_reconciliations(bucket_id,state) VALUES(1,'scanning');
INSERT INTO object_storage_bucket_usage(bucket_id,granted_bytes,granted_keys) VALUES(1,5,1);`)
	if err != nil {
		t.Fatal(err)
	}
	sections := make([][]string, 0, 2)
	for _, name := range []string{"20261002110000000_object_write_history_recovery.sql", "20261002110000001_object_version_reclamation_fence.sql"} {
		raw, e := migrations.FS.ReadFile(name)
		if e != nil {
			t.Fatal(e)
		}
		s := strings.SplitN(string(raw), "-- +goose Down", 2)
		sections = append(sections, s)
		if _, err = pool.Exec(ctx, s[0]); err != nil {
			t.Fatal(name, err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_cursor=repeat('x',8193) WHERE id=1`); err == nil {
		t.Fatal("unbounded cursor")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_cursor=repeat('é',4097) WHERE id=1`); err == nil {
		t.Fatal("cursor byte limit bypassed")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_versions_observed=true WHERE id=1;UPDATE object_upload_completions SET recovery_versions_observed=false WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	var observed bool
	if err = pool.QueryRow(ctx, `SELECT recovery_versions_observed FROM object_upload_completions WHERE id=1`).Scan(&observed); err != nil || !observed {
		t.Fatal("version latch erased", observed, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET baseline_bytes=0,baseline_keys=0,granted_bytes=0,granted_keys=0 WHERE bucket_id=1`); err == nil {
		t.Fatal("legacy rebase bypassed fence")
	}
	if _, err = pool.Exec(ctx, sections[1][1]); err == nil {
		t.Fatal("rollback removed retained-version accounting fence")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET state='blocked',last_error_code='version_accounting_required' WHERE bucket_id=1`); err != nil {
		t.Fatal(err)
	}
	// A different bucket still uses ordinary current-object reconciliation.
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_bucket_usage(bucket_id,granted_bytes) VALUES(2,5);UPDATE object_storage_bucket_usage SET granted_bytes=0 WHERE bucket_id=2`); err != nil {
		t.Fatal("cross-bucket accounting fence", err)
	}
	// Rollback round trip is supported in an empty deployment.
	if _, err = pool.Exec(ctx, `DELETE FROM object_upload_completions;DELETE FROM object_storage_capacity_reconciliations`); err != nil {
		t.Fatal(err)
	}
	for _, s := range [][]string{sections[1], sections[0]} {
		if _, err = pool.Exec(ctx, s[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range sections {
		if _, err = pool.Exec(ctx, s[0]); err != nil {
			t.Fatal("down/up", err)
		}
	}
}
