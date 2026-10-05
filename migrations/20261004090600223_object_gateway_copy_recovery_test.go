package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// adr: 536
func TestObjectGatewayCopyRecoveryMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261004090600223_object_gateway_copy_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,route_id uuid,status text,write_phase text,idempotency_key text DEFAULT '',request_fingerprint text DEFAULT '',origin text NOT NULL DEFAULT 'route',CONSTRAINT object_upload_completions_origin_check CHECK(origin IN ('route','gateway')),CONSTRAINT object_upload_gateway_receipt CHECK(origin<>'gateway' OR route_id IS NULL));
CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,state text DEFAULT 'pending');
CREATE TABLE object_storage_key_grants(last_write_id uuid,reclaimable boolean);
INSERT INTO object_upload_completions(id,status,write_phase) VALUES('00000000-0000-0000-0000-000000000001','pending','dispatched');
INSERT INTO object_upload_completions(id,status,write_phase,origin) VALUES('00000000-0000-0000-0000-000000000002','pending','dispatched','gateway');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`INSERT INTO object_upload_completions(id,status,write_phase,origin) VALUES('00000000-0000-0000-0000-000000000003','pending','prepared','gateway_copy')`,
		`UPDATE object_upload_completions SET source_key='source' WHERE origin='gateway'`,
	} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("missing source guard", query)
		}
	}
	_, err = pool.Exec(ctx, `INSERT INTO object_upload_completions(id,status,write_phase,origin,source_key,source_etag) VALUES('00000000-0000-0000-0000-000000000003','pending','prepared','gateway_copy','source','"source"');
INSERT INTO object_storage_write_admissions(id) VALUES('00000000-0000-0000-0000-000000000003');
INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000003',true);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE object_upload_completions SET source_etag='' WHERE origin='gateway_copy'`,
		`UPDATE object_upload_completions SET source_etag=chr(10) WHERE origin='gateway_copy'`,
		`UPDATE object_upload_completions SET source_etag=repeat('x',257) WHERE origin='gateway_copy'`,
		`UPDATE object_upload_completions SET route_id='00000000-0000-0000-0000-000000000004' WHERE origin='gateway_copy'`,
		`UPDATE object_upload_completions SET idempotency_key='replay' WHERE origin='gateway_copy'`,
		`UPDATE object_upload_completions SET write_phase='untracked' WHERE origin='gateway_copy'`,
	} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("missing copy guard", query)
		}
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased unresolved copy")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET status='completed',write_phase='settled' WHERE origin='gateway_copy'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased pending copy journal")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_write_admissions SET state='settled'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	var safe bool
	if err = pool.QueryRow(ctx, `SELECT reclaimable FROM object_storage_key_grants`).Scan(&safe); err != nil || safe {
		t.Fatal("rollback invented refund", safe, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_upload_completions`).Scan(&count); err != nil || count != 2 {
		t.Fatal("rollback erased PUT/route receipts", count, err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
