package migrations_test

import (
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"strings"
	"testing"
)

// adr: 398
func TestObjectVersionInventoryMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,bucket_id uuid,recovery_versions_observed boolean DEFAULT false);
CREATE TABLE object_storage_capacity_reconciliations(id uuid PRIMARY KEY,bucket_id uuid,state text,lease_until timestamptz);
CREATE TABLE object_storage_bucket_usage(bucket_id uuid PRIMARY KEY,baseline_bytes bigint DEFAULT 0,baseline_keys bigint DEFAULT 0,granted_bytes bigint DEFAULT 0,granted_keys bigint DEFAULT 0,observed_bytes bigint DEFAULT 0,observed_keys bigint DEFAULT 0,observed_at timestamptz);
CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,bucket_id uuid);
CREATE TABLE object_storage_key_grants(bucket_id uuid,key_hash text,max_bytes bigint);
CREATE FUNCTION fence_object_version_reclamation() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$;
CREATE TRIGGER object_version_reclamation_fence BEFORE UPDATE ON object_storage_bucket_usage FOR EACH ROW EXECUTE FUNCTION fence_object_version_reclamation();
INSERT INTO object_storage_bucket_usage(bucket_id,granted_bytes) VALUES('00000000-0000-0000-0000-000000000001',5),('00000000-0000-0000-0000-000000000002',5);
INSERT INTO object_upload_completions VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001',true);
INSERT INTO object_storage_capacity_reconciliations VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','scanning',now()+interval '1 minute');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261002120000000_object_version_inventory.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE object_storage_bucket_usage SET granted_bytes=0 WHERE bucket_id='00000000-0000-0000-0000-000000000001'`,
		`UPDATE object_storage_capacity_reconciliations SET inventory_cursor=repeat('é',4097)`,
		`UPDATE object_storage_capacity_reconciliations SET inventory_verified=true`,
		`UPDATE object_storage_capacity_reconciliations SET scanned_pages=1001`,
		`UPDATE object_storage_capacity_reconciliations SET scanned_versions=1000001`,
		`INSERT INTO object_storage_version_inventory_entries VALUES('00000000-0000-0000-0000-000000000001','bad',1)`,
	} {
		if _, err = pool.Exec(ctx, q); err == nil {
			t.Fatal("invalid native inventory accepted", q)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET inventory_scope='all_versions',inventory_verified=true;
UPDATE object_storage_bucket_usage SET inventory_scope='all_versions',baseline_bytes=9,baseline_keys=2,granted_bytes=0 WHERE bucket_id='00000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal("verified native rebase rejected", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_bucket_usage SET inventory_scope='current' WHERE bucket_id='00000000-0000-0000-0000-000000000001'`); err == nil {
		t.Fatal("native accounting reverted")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_write_admissions(id,bucket_id,native_version,native_bytes) VALUES(gen_random_uuid(),'00000000-0000-0000-0000-000000000001',true,1)`); err == nil {
		t.Fatal("native admission bypassed scan fence")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET state='completed';INSERT INTO object_storage_write_admissions(id,bucket_id,native_version,native_bytes) VALUES(gen_random_uuid(),'00000000-0000-0000-0000-000000000001',true,1)`); err != nil {
		t.Fatal("native admission rejected", err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000001',repeat('a',64),1)`); err == nil {
		t.Fatal("legacy key grant bypassed native accounting")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000002',repeat('a',64),1)`); err != nil {
		t.Fatal("foreign bucket fenced", err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback destroyed native accounting")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_storage_capacity_reconciliations;DELETE FROM object_storage_bucket_usage WHERE inventory_scope='all_versions';DELETE FROM object_upload_completions;DELETE FROM object_storage_write_admissions`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
