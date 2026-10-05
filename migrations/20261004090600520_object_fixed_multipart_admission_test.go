package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 560
func TestObjectFixedMultipartAdmissionMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,account_id uuid,app_id uuid,bucket_id uuid,object_key text,size_bytes bigint,part_size_bytes bigint,part_count int,state text);
 CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,bucket_id uuid,key_hash text,kind text,multipart_upload_id uuid,route_receipt boolean,native_version boolean,native_bytes bigint,state text);
 INSERT INTO object_storage_multipart_uploads VALUES('00000000-0000-0000-0000-000000000001',gen_random_uuid(),gen_random_uuid(),gen_random_uuid(),'object',5,5,1,'active');`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261004090600520_object_fixed_multipart_admission.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var fixed bool
	if err = pool.QueryRow(ctx, `SELECT fixed_admission FROM object_storage_multipart_uploads`).Scan(&fixed); err != nil || fixed {
		t.Fatal("upgrade changed legacy accounting", fixed, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_uploads SELECT gen_random_uuid(),account_id,app_id,bucket_id,'unbound',5,5,1,'active',true FROM object_storage_multipart_uploads LIMIT 1`); err == nil {
		t.Fatal("missing full-object admission committed")
	}
	// The binding is deferred: initialization publishes both records atomically.
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_multipart_uploads SELECT '00000000-0000-0000-0000-000000000002',account_id,app_id,bucket_id,'new',5,5,1,'active',true FROM object_storage_multipart_uploads LIMIT 1;
 INSERT INTO object_storage_write_admissions SELECT id,bucket_id,encode(sha256(convert_to(object_key,'UTF8')),'hex'),'multipart',id,false,true,size_bytes,'pending' FROM object_storage_multipart_uploads WHERE fixed_admission;`); err != nil {
		t.Fatal("atomic admission rejected", err)
	}
	for _, statement := range []string{
		`UPDATE object_storage_multipart_uploads SET fixed_admission=false WHERE fixed_admission`,
		`UPDATE object_storage_multipart_uploads SET size_bytes=6 WHERE fixed_admission`,
		`DELETE FROM object_storage_multipart_uploads WHERE fixed_admission`,
		`UPDATE object_storage_write_admissions SET native_bytes=0`,
		`DELETE FROM object_storage_write_admissions`,
		sections[1],
	} {
		if _, err = pool.Exec(ctx, statement); err == nil {
			t.Fatal("live admission lost its capacity or fencing", statement)
		}
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET state='completed' WHERE fixed_admission`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("drained rollback", err)
	}
	var bytes int64
	if err = pool.QueryRow(ctx, `SELECT native_bytes FROM object_storage_write_admissions`).Scan(&bytes); err != nil || bytes != 5 {
		t.Fatal("rollback erased retained capacity", bytes, err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("cold reapply", err)
	}
}
