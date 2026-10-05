package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 547
func TestObjectDeletionMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, e := pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY,state text);CREATE TABLE object_storage_write_admissions(bucket_id uuid);CREATE TABLE object_storage_key_grants(bucket_id uuid);CREATE TABLE object_storage_multipart_uploads(bucket_id uuid);CREATE TABLE object_bucket_versioning(bucket_id uuid,state text);CREATE TABLE object_storage_capacity_reconciliations(bucket_id uuid,state text);CREATE TABLE object_storage_bucket_usage(bucket_id uuid,baseline_bytes bigint,baseline_keys bigint,observed_bytes bigint,observed_keys bigint,observed_at timestamptz,inventory_scope text);INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','ready');`)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := migrations.FS.ReadFile("20261004090600362_object_deletions.sql")
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, e = pool.Exec(ctx, parts[0]); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `INSERT INTO object_deletions(id,bucket_id,object_key,state,lease_token,lease_until) VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','key','prepared','worker',now()+interval '2 minutes')`); e != nil {
		t.Fatal(e)
	}
	for _, query := range []string{
		`INSERT INTO object_storage_write_admissions VALUES('00000000-0000-0000-0000-000000000001')`,
		`INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000001')`,
		`INSERT INTO object_storage_multipart_uploads VALUES('00000000-0000-0000-0000-000000000001')`,
		`INSERT INTO object_bucket_versioning VALUES('00000000-0000-0000-0000-000000000001','waiting')`,
		`INSERT INTO object_storage_capacity_reconciliations VALUES('00000000-0000-0000-0000-000000000001','waiting')`,
		`UPDATE object_buckets SET state='deleting'`,
		`DELETE FROM object_buckets`,
	} {
		if _, e = pool.Exec(ctx, query); e == nil {
			t.Fatal("pending deletion fence bypassed", query)
		}
	}
	if _, e = pool.Exec(ctx, `UPDATE object_deletions SET state='dispatched';UPDATE object_deletions SET state='failed',last_error_code='preparation_expired',lease_token='',lease_until=NULL`); e == nil {
		t.Fatal("timer dropped dispatch proof")
	}
	if _, e = pool.Exec(ctx, parts[1]); e == nil {
		t.Fatal("rollback discarded intent")
	}
	if _, e = pool.Exec(ctx, `DELETE FROM object_deletions`); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, parts[1]); e != nil {
		t.Fatal("empty rollback", e)
	}
	if _, e = pool.Exec(ctx, parts[0]); e != nil {
		t.Fatal("down/up", e)
	}
}
