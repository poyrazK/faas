package migrations_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectCapacityReconciliationMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261001133215842_object_capacity_reconciliation.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE object_buckets(id uuid PRIMARY KEY,state text);
 CREATE TABLE object_storage_multipart_uploads(id uuid PRIMARY KEY,bucket_id uuid);
 CREATE TABLE object_storage_key_grants(bucket_id uuid,key_hash text,max_bytes bigint,PRIMARY KEY(bucket_id,key_hash));
 INSERT INTO object_buckets VALUES('00000000-0000-0000-0000-000000000001','ready');
 INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000001',repeat('a',64),10);`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var reclaimable bool
	if err = pool.QueryRow(ctx, `SELECT reclaimable FROM object_storage_key_grants`).Scan(&reclaimable); err != nil || reclaimable {
		t.Fatal("legacy grant upgraded", err)
	}
	for _, query := range []string{
		`INSERT INTO object_storage_write_admissions(id,bucket_id,key_hash,kind) VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001',repeat('b',64),'proxy');`,
		`INSERT INTO object_storage_key_grants(bucket_id,key_hash,max_bytes,reclaimable,last_write_id) VALUES('00000000-0000-0000-0000-000000000001',repeat('b',64),20,true,'00000000-0000-0000-0000-000000000002');`,
		`INSERT INTO object_storage_capacity_reconciliations(id,bucket_id,deadline_at) VALUES('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001',now()+interval '1 hour');`,
	} {
		if _, err = pool.Exec(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`UPDATE object_storage_key_grants SET max_bytes=max_bytes+1`,
		`INSERT INTO object_storage_multipart_uploads VALUES('00000000-0000-0000-0000-000000000004','00000000-0000-0000-0000-000000000001')`,
		`UPDATE object_buckets SET state='deleting'`,
		`UPDATE object_storage_capacity_reconciliations SET state='provider-secret'`,
	} {
		_, err = pool.Exec(ctx, query)
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "23514" {
			t.Fatal("missing DB guard", err)
		}
	}
	// Rollback must not erase an active fence. Multi-statement Exec rolls back.
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased an active reconciliation")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_capacity_reconciliations SET state='cancelled',finished_at=now();UPDATE object_storage_key_grants SET max_bytes=max_bytes;`); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT reclaimable FROM object_storage_key_grants WHERE key_hash=repeat('b',64)`).Scan(&reclaimable); err != nil || reclaimable {
		t.Fatal("old SQL writer did not downgrade", err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
