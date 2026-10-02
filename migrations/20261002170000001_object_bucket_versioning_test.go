package migrations_test

import (
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"strings"
	"testing"
)

// adr: 403
func TestObjectBucketVersioningMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY,state text);
 CREATE TABLE object_storage_capacity_reconciliations(id uuid PRIMARY KEY,bucket_id uuid,state text,inventory_scope text,inventory_verified boolean,lease_until timestamptz);
 CREATE TABLE object_storage_bucket_usage(bucket_id uuid PRIMARY KEY,baseline_bytes bigint,baseline_keys bigint,granted_bytes bigint,granted_keys bigint,observed_bytes bigint,observed_keys bigint,observed_at timestamptz,inventory_scope text);
 INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','ready');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261002170000001_object_bucket_versioning.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_bucket_versioning(bucket_id,desired_status,state) VALUES('00000000-0000-0000-0000-000000000001','Enabled','waiting');
 UPDATE object_bucket_versioning SET versions_required=true,dispatched=true,propagation_until=now()+interval '15 minutes';
 UPDATE object_bucket_versioning SET versions_required=false;`); err != nil {
		t.Fatal(err)
	}
	var sticky bool
	if err = pool.QueryRow(ctx, `SELECT versions_required FROM object_bucket_versioning`).Scan(&sticky); err != nil || !sticky {
		t.Fatal("lost version accounting", sticky, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_versioning SET observed_status='Enabled',state='ready'`); err == nil {
		t.Fatal("unverified cutover became ready")
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback discarded versioning intent")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_bucket_versioning`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
