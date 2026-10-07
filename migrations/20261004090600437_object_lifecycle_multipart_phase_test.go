package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 550
func TestObjectLifecycleMultipartPhaseMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);
 CREATE TABLE object_buckets(id uuid PRIMARY KEY,account_id uuid,app_id uuid,state text);
 CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,account_id uuid,app_id uuid,bucket_id uuid,object_key text,provider_upload_id text,state text,created_at timestamptz,lease_until timestamptz);
 INSERT INTO accounts VALUES('00000000-0000-4000-8000-000000000001');
 INSERT INTO object_buckets VALUES('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000003','ready');
 INSERT INTO object_storage_multipart_uploads VALUES('00000000-0000-4000-8000-000000000004','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000003','00000000-0000-4000-8000-000000000002','tmp/key','native/+?','active',now()-interval '4 days',NULL);`); err != nil {
		t.Fatal(err)
	}
	old, err := migrations.FS.ReadFile("20261004090600373_object_lifecycle_scans.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, strings.SplitN(string(old), "-- +goose Down", 2)[0]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_bucket_lifecycle VALUES('00000000-0000-4000-8000-000000000002',1,'[{"id":"cleanup","status":"Enabled","filter":{"prefix":"tmp/"},"abort_incomplete_multipart_days":1}]',now(),now());
 INSERT INTO object_lifecycle_scans(id,bucket_id,revision,rules,lease_token,lease_until,retry_at,created_at,updated_at)
 SELECT '00000000-0000-4000-8000-000000000005',bucket_id,revision,rules,'scan',now()+interval '2 minutes',now(),now(),now() FROM object_bucket_lifecycle;`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261004090600437_object_lifecycle_multipart_phase.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var phase string
	if err = pool.QueryRow(ctx, `SELECT phase FROM object_lifecycle_scans`).Scan(&phase); err != nil || phase != "objects" {
		t.Fatal("upgrade rewrote existing scan", phase, err)
	}
	admit := `UPDATE object_storage_multipart_uploads SET state='aborting',lifecycle_scan_id='00000000-0000-4000-8000-000000000005',lifecycle_binding=jsonb_build_object('scan_id','00000000-0000-4000-8000-000000000005','scan_token','scan','rule_id','cleanup','expected_provider_upload_id',provider_upload_id,'expected_created_at',created_at)`
	if _, err = pool.Exec(ctx, admit); err == nil {
		t.Fatal("object-phase scan admitted multipart abort")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET phase='multipart'`); err != nil {
		t.Fatal("valid phase transition", err)
	}
	for _, statement := range []string{
		strings.Replace(admit, "'scan_token','scan'", "'scan_token','stale'", 1),
		strings.Replace(admit, "'rule_id','cleanup'", "'rule_id','foreign'", 1),
		strings.Replace(admit, "'expected_created_at',created_at", "'expected_created_at',created_at-interval '1 second'", 1),
		strings.Replace(admit, "'expected_provider_upload_id',provider_upload_id", "'expected_provider_upload_id','stale'", 1),
		strings.Replace(admit, "SET state=", "SET object_key='other/key',state=", 1),
		`UPDATE object_lifecycle_scans SET phase='objects'`,
		`UPDATE object_lifecycle_scans SET scanned_uploads=1`,
		`UPDATE object_lifecycle_scans SET last_upload_id='00000000-0000-4000-8000-000000000004'`,
		`UPDATE object_lifecycle_scans SET last_key='key',scanned_keys=1`,
	} {
		if _, err = pool.Exec(ctx, statement); err == nil {
			t.Fatal("protected rewrite accepted", statement)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, admit); err == nil {
		t.Fatal("young or post-cutoff upload admitted")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET created_at=now()-interval '4 days';`+admit); err != nil {
		t.Fatal("valid atomic admission", err)
	}
	for _, statement := range []string{
		`UPDATE object_storage_multipart_uploads SET lifecycle_scan_id=NULL,lifecycle_binding='{}'`,
		`UPDATE object_storage_multipart_uploads SET lifecycle_binding=jsonb_set(lifecycle_binding,'{rule_id}','"replacement"')`,
		`UPDATE object_storage_multipart_uploads SET provider_upload_id='other'`,
		`UPDATE object_storage_multipart_uploads SET created_at=created_at-interval '1 second'`,
		`UPDATE object_storage_multipart_uploads SET object_key='other'`,
		`UPDATE object_storage_multipart_uploads SET state='active'`,
		parts[1],
	} {
		if _, err = pool.Exec(ctx, statement); err == nil {
			t.Fatal("admission history lost", statement)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET last_upload_id='00000000-0000-4000-8000-000000000004',scanned_uploads=1,lease_token='',lease_until=NULL;
 UPDATE object_lifecycle_scans SET state='completed',finished_at=clock_timestamp(),updated_at=clock_timestamp();
 UPDATE object_bucket_lifecycle SET revision=2,rules='[]';
 UPDATE object_storage_multipart_uploads SET state='aborted';`); err != nil {
		t.Fatal("terminal recovery after rule replacement", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_lifecycle_scans SET retry_at=retry_at+interval '1 second'`); err == nil {
		t.Fatal("terminal scan history rewritten")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_storage_multipart_uploads; DELETE FROM object_lifecycle_scans; DELETE FROM object_bucket_lifecycle;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("empty rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("cold reapply", err)
	}
}
