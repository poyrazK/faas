package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectBucketWriteCleanupMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004183054338_object_bucket_write_cleanup_fence.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY,state text);
 CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,state text);
 CREATE TABLE object_storage_write_admissions(bucket_id uuid,state text,multipart_upload_id uuid);
 INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','ready');
 INSERT INTO object_storage_write_admissions VALUES('00000000-0000-0000-0000-000000000001','pending',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = pool.Exec(ctx, sections[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback removed a pending write fence")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_write_admissions SET state='settled'; UPDATE object_buckets SET state='deleting'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback reopened admission during cleanup")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET state='ready'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("settled rollback", err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("reapply", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_buckets SET state='deleting'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO object_storage_write_admissions VALUES('00000000-0000-0000-0000-000000000001','pending',NULL)`); err == nil {
		t.Fatal("reapplied fence allowed an old writer during cleanup")
	}
}
