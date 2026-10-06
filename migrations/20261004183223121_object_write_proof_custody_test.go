package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectWriteProofCustodyMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004183223121_object_write_proof_custody.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);
 CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,state text);
 CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,state text);
 CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,bucket_id uuid,key_hash text,state text,multipart_upload_id uuid);
 CREATE TABLE object_storage_key_grants(bucket_id uuid,key_hash text,last_write_id uuid);
 INSERT INTO accounts VALUES('00000000-0000-0000-0000-000000000001');
 INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','ready');
 INSERT INTO object_storage_write_admissions VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','key','pending',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback removed custody of a dispatched write")
	}
	for _, query := range []string{
		`INSERT INTO object_storage_write_admissions SELECT '00000000-0000-0000-0000-000000000003',bucket_id,key_hash,'pending',NULL FROM object_storage_write_admissions`,
		`INSERT INTO object_storage_key_grants SELECT bucket_id,key_hash,NULL FROM object_storage_write_admissions`,
	} {
		_, err = pool.Exec(ctx, query)
		var check *pgconn.PgError
		if !errors.As(err, &check) || check.ConstraintName != "object_write_key_fenced" {
			t.Fatal("old writer bypassed custody", err)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_write_admissions SET state='settled'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("settled rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
}

func TestObjectOwnedCleanupRetryMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004183337695_object_owned_cleanup_retry_codes.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY,status text);
 CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,state text,last_error_code text CHECK(last_error_code IN ('','temporary','configuration','conflict','invalid')));
 INSERT INTO accounts VALUES('00000000-0000-0000-0000-000000000001','deleted_pending');
 INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000001','ready','');`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','provisioning','')`)
	var check *pgconn.PgError
	if !errors.As(err, &check) || check.ConstraintName != "object_bucket_account_cleanup_fenced" {
		t.Fatal("old writer restored expired resources", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET state='deleting',last_error_code='protected'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback forgot protected cleanup")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET state='ready',last_error_code=''`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("settled rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
