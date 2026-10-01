package migrations_test

import (
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"strings"
	"testing"
)

// adr: 393
func TestObjectGatewayUploadRecoveryMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261001161415163_object_gateway_upload_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,route_id uuid,status text,write_phase text,idempotency_key text DEFAULT '',request_fingerprint text DEFAULT '');
 CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,state text DEFAULT 'pending',kind text DEFAULT 'proxy',route_receipt boolean DEFAULT true);
 CREATE TABLE object_storage_key_grants(last_write_id uuid,reclaimable boolean);
 INSERT INTO object_upload_completions VALUES('00000000-0000-0000-0000-000000000001',NULL,'pending','dispatched','','');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var origin string
	if err = pool.QueryRow(ctx, `SELECT origin FROM object_upload_completions`).Scan(&origin); err != nil || origin != "route" {
		t.Fatal("legacy classified as gateway", origin, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO object_upload_completions(id,status,write_phase,origin) VALUES('00000000-0000-0000-0000-000000000002','pending','prepared','gateway');
 INSERT INTO object_storage_write_admissions(id) VALUES('00000000-0000-0000-0000-000000000002');
 INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000002',true);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE object_upload_completions SET origin='future'`,
		`UPDATE object_upload_completions SET route_id='00000000-0000-0000-0000-000000000003' WHERE origin='gateway'`,
		`UPDATE object_upload_completions SET idempotency_key='replay' WHERE origin='gateway'`,
		`UPDATE object_upload_completions SET write_phase='untracked' WHERE origin='gateway'`,
	} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("missing receipt guard", query)
		}
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased unresolved receipt")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET status='completed',write_phase='settled' WHERE origin='gateway'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback erased pending journal")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_storage_write_admissions SET state='settled'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	var safe bool
	if err = pool.QueryRow(ctx, `SELECT reclaimable FROM object_storage_key_grants`).Scan(&safe); err != nil || safe {
		t.Fatal("rollback invented reclamation", safe, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_upload_completions`).Scan(&count); err != nil || count != 1 {
		t.Fatal("rollback lost route receipt", count, err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
