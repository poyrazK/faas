package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectVersionProtectionMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004193552286_object_version_protection.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);CREATE TABLE apps(id uuid PRIMARY KEY);
CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,app_id uuid,state text);
CREATE TABLE object_bucket_object_lock(bucket_id uuid,state text,observed_known bool,native_enabled_observed bool,observed_snapshot jsonb,revision bigint,dispatched bool);
CREATE TABLE object_bucket_versioning(bucket_id uuid,revision bigint,dispatched bool);
CREATE TABLE object_bucket_encryption(bucket_id uuid,state text,revision bigint,dispatched bool);
CREATE TABLE object_deletions(bucket_id uuid,state text);
CREATE TABLE object_storage_capacity_reconciliations(bucket_id uuid);
CREATE TABLE object_storage_write_admissions(bucket_id uuid);
CREATE TABLE object_upload_completions(bucket_id uuid);
CREATE TABLE object_storage_multipart_uploads(bucket_id uuid);
CREATE TABLE object_storage_key_grants(bucket_id uuid);
CREATE TABLE object_version_references(id uuid,bucket_id uuid,object_key text,native_version_id text);
CREATE FUNCTION object_lock_versioning_ready(uuid) RETURNS bool LANGUAGE SQL AS $$SELECT true$$;
CREATE FUNCTION object_lock_drained(uuid) RETURNS bool LANGUAGE SQL AS $$SELECT true$$;
INSERT INTO accounts VALUES('00000000-0000-4000-8000-000000000001');INSERT INTO apps VALUES('00000000-0000-4000-8000-000000000002');
INSERT INTO object_buckets VALUES('00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','ready');
INSERT INTO object_bucket_object_lock VALUES('00000000-0000-4000-8000-000000000003','ready',true,true,'{"enabled":true}',1,false);
INSERT INTO object_version_references VALUES('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000003','key','private');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO object_version_protection(id,bucket_id,account_id,app_id,object_key,public_version_id,native_version_id,intent)
VALUES('00000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','key','00000000-0000-4000-8000-000000000004','private','{"id":"00000000-0000-4000-8000-000000000005","bucket_id":"00000000-0000-4000-8000-000000000003","key":"key","version_id":"00000000-0000-4000-8000-000000000004","kind":"legal_hold","legal_hold":{"status":"ON"}}');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("replay lost pending custody", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded receipt")
	}
	for _, query := range []string{
		`DELETE FROM object_version_protection`,
		`UPDATE object_version_protection SET native_version_id='different'`,
		`UPDATE object_version_protection SET state='ready'`,
		`UPDATE object_version_protection SET dispatched=true`,
		`INSERT INTO object_deletions VALUES('00000000-0000-4000-8000-000000000003','prepared')`,
		`UPDATE object_buckets SET state='deleting'`,
		`INSERT INTO object_storage_key_grants VALUES('00000000-0000-4000-8000-000000000003')`,
		`INSERT INTO object_storage_write_admissions VALUES('00000000-0000-4000-8000-000000000003')`,
		`INSERT INTO object_storage_multipart_uploads VALUES('00000000-0000-4000-8000-000000000003')`,
		`INSERT INTO object_storage_capacity_reconciliations VALUES('00000000-0000-4000-8000-000000000003')`,
	} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("guard bypass", query)
		}
	}
	_, err = pool.Exec(ctx, `UPDATE object_version_protection SET state='applying',lease_token='worker',lease_until=now()+interval '2 minutes';UPDATE object_version_protection SET dispatched=true;UPDATE object_version_protection SET state='ready',lease_token='',lease_until=NULL;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded terminal retry identity")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET state='deleted';DELETE FROM object_version_protection;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
