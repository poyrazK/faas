package migrations_test

import (
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"strings"
	"testing"
)

// adr: 548
func TestImmutableDeletionInventoryMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, e := pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY,state text);CREATE TABLE object_storage_write_admissions(bucket_id uuid);CREATE TABLE object_storage_key_grants(bucket_id uuid);CREATE TABLE object_storage_multipart_uploads(bucket_id uuid);CREATE TABLE object_bucket_versioning(bucket_id uuid,state text);CREATE TABLE object_storage_capacity_reconciliations(bucket_id uuid,state text);CREATE TABLE object_storage_bucket_usage(bucket_id uuid,baseline_bytes bigint,baseline_keys bigint,observed_bytes bigint,observed_keys bigint,observed_at timestamptz,inventory_scope text);CREATE TABLE object_version_references(id uuid,bucket_id uuid,object_key text,native_version_id text);INSERT INTO object_buckets VALUES('00000000-0000-4000-8000-000000000001','ready');INSERT INTO object_version_references VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','key','native');`); e != nil {
		t.Fatal(e)
	}
	previous, e := migrations.FS.ReadFile("20261004090600362_object_deletions.sql")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, strings.SplitN(string(previous), "-- +goose Down", 2)[0]); e != nil {
		t.Fatal(e)
	}
	raw, e := migrations.FS.ReadFile("20261004090600401_immutable_deletion_inventory_coordination.sql")
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, e = pool.Exec(ctx, parts[0]); e != nil {
		t.Fatal(e)
	}
	const insert = `INSERT INTO object_deletions(id,bucket_id,object_key,selector,target_provider_version_id,state) VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','key','00000000-0000-4000-8000-000000000003','native','prepared')`
	if _, e = pool.Exec(ctx, strings.Replace(insert, "'native'", "'wrong'", 1)); e == nil {
		t.Fatal("unowned target accepted")
	}
	if _, e = pool.Exec(ctx, `INSERT INTO object_storage_capacity_reconciliations VALUES('00000000-0000-4000-8000-000000000001','waiting')`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, insert); e == nil {
		t.Fatal("inventory fence bypassed")
	}
	if _, e = pool.Exec(ctx, `DELETE FROM object_storage_capacity_reconciliations`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, insert); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET target_provider_version_id='different'`); e == nil {
		t.Fatal("private target changed")
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET state='dispatched'`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET recovery_claimed=true`); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{`UPDATE object_deletions SET recovery_claimed=false`, `UPDATE object_deletions SET state='failed',last_error_code='provider_rejected'`} {
		if _, e = pool.Exec(ctx, query); e == nil {
			t.Fatal("recovery proof erased", query)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET state='completed',version_id='00000000-0000-4000-8000-000000000004',provider_version_id='native'`); e == nil {
		t.Fatal("wrong public completion accepted")
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET state='completed',version_id=selector,provider_version_id=target_provider_version_id`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, parts[1]); e == nil {
		t.Fatal("rollback discarded immutable receipts")
	}
	if _, e = pool.Exec(ctx, `DELETE FROM object_deletions`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, parts[1]); e != nil {
		t.Fatal("down", e)
	}
	if _, e = pool.Exec(ctx, parts[0]); e != nil {
		t.Fatal("up after down", e)
	}
}
