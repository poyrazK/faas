package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 420
func TestObjectCopySourceMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);
 CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,backend_id text,backend_fingerprint text,state text);
 CREATE TABLE object_storage_s3_credentials(id uuid PRIMARY KEY,account_id uuid,bucket_id uuid,status text,permission text,url_request jsonb,rotation_parent_id uuid);
 CREATE TABLE object_deletions(bucket_id uuid,state text);
 CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,account_id uuid,bucket_id uuid,subject_id text,origin text,source_key text,source_etag text,write_phase text,app_id uuid,object_key text,bytes bigint);
 CREATE TABLE object_storage_write_admissions(id uuid,bucket_id uuid,state text);
 CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,account_id uuid,bucket_id uuid,state text,expires_at timestamptz);
 CREATE TABLE object_storage_multipart_part_grants(upload_id uuid,part_number int,transfer_token text,PRIMARY KEY(upload_id,part_number));
 INSERT INTO accounts VALUES('00000000-0000-4000-8000-000000000001');
 INSERT INTO object_buckets SELECT '00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','local','fingerprint','ready';
 INSERT INTO object_buckets SELECT '00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000001','local','fingerprint','ready';
 INSERT INTO object_storage_s3_credentials SELECT '00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','active','write',NULL,NULL;`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261004033200416_object_s3_copy_source_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	set := `INSERT INTO object_s3_copy_source_grants(id,account_id,credential_id,bucket_id,source_bucket_id,prefix)
 SELECT '00000000-0000-4000-8000-000000000005',account_id,id,bucket_id,'00000000-0000-4000-8000-000000000003','allowed/' FROM object_storage_s3_credentials;`
	if _, err = pool.Exec(ctx, set); err != nil {
		t.Fatal(err)
	}
	replay := strings.TrimSuffix(set, ";") + " WHERE true ON CONFLICT(credential_id,source_bucket_id) DO UPDATE SET updated_at=excluded.updated_at;"
	if _, err = pool.Exec(ctx, replay); err != nil {
		t.Fatal("current identity retry failed", err)
	}
	for _, mutation := range []string{
		`UPDATE object_s3_copy_source_epochs SET id='00000000-0000-4000-8000-000000000007'`,
		`DELETE FROM object_s3_copy_source_epochs`,
	} {
		if _, err = pool.Exec(ctx, mutation); err == nil {
			t.Fatal("published identity history was mutable", mutation)
		}
	}
	insert := `INSERT INTO object_upload_completions(id,account_id,bucket_id,subject_id,origin,source_key,source_etag,write_phase,source_bucket_id,source_copy_grant_id) VALUES('00000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000004','gateway_copy','allowed/key','"source"','prepared','00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000005');`
	if _, err = pool.Exec(ctx, strings.Replace(insert, "allowed/key", "outside/key", 1)); err == nil {
		t.Fatal("database admitted ungranted key")
	}
	if _, err = pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_s3_copy_source_grants SET prefix='new/'`); err == nil {
		t.Fatal("prefix changed without new epoch")
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("live grants allowed rollback")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_s3_copy_source_grants`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, set); err == nil {
		t.Fatal("revoked identity was reused by direct SQL")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET write_phase='dispatched'`); err == nil {
		t.Fatal("revoked grant dispatched")
	}
	if _, err = pool.Exec(ctx, strings.ReplaceAll(set, "000000000005", "000000000007")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_s3_copy_source_grants SET id='00000000-0000-4000-8000-000000000005'`); err == nil {
		t.Fatal("update reused historical identity")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET write_phase='dispatched'`); err == nil {
		t.Fatal("recreated grant revived old receipt")
	}
	if _, err = pool.Exec(ctx, strings.ReplaceAll(strings.ReplaceAll(insert, "000000000006", "000000000008"), "000000000005", "000000000007")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET write_phase='dispatched' WHERE id='00000000-0000-4000-8000-000000000008'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET source_key='forged'`); err == nil {
		t.Fatal("receipt provenance changed")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_uploads SELECT '00000000-0000-4000-8000-000000000009',account_id,bucket_id,'active',now()+interval '1 hour' FROM object_storage_s3_credentials;
 INSERT INTO object_storage_multipart_part_grants VALUES('00000000-0000-4000-8000-000000000009',1,'part','00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000004','allowed/key');`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET source_key='forged'`); err == nil {
		t.Fatal("part provenance changed")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_s3_copy_source_grants;
 UPDATE object_upload_completions SET write_phase='settled';`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("live cross-copy part allowed rollback")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_part_grants SET transfer_token=NULL`); err != nil {
		t.Fatal("revocation blocked existing part settlement", err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("drained rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("cold reapply", err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_upload_completions WHERE source_bucket_id IS NULL AND write_phase='settled'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rollback lost settled receipts", count, err)
	}
	if _, err = pool.Exec(ctx, set); err != nil {
		t.Fatal("cold grant creation", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM accounts`); err != nil {
		t.Fatal("account deletion could not remove private history", err)
	}
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM object_s3_copy_source_epochs)+(SELECT count(*) FROM object_s3_copy_source_grants)`).Scan(&count); err != nil || count != 0 {
		t.Fatal("account deletion retained grant history", count, err)
	}
}
