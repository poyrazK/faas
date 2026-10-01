package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestObjectRouteUploadTrackingMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261001145449059_object_route_upload_tracking.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	_, err = pool.Exec(ctx, `CREATE TABLE accounts(id uuid PRIMARY KEY);CREATE TABLE apps(id uuid PRIMARY KEY);CREATE TABLE object_buckets(id uuid PRIMARY KEY);
 CREATE TABLE object_upload_routes(id uuid PRIMARY KEY);
 CREATE TABLE object_upload_completions(id uuid PRIMARY KEY,route_id uuid NOT NULL REFERENCES object_upload_routes(id) ON DELETE CASCADE,status text NOT NULL);
 CREATE TABLE object_storage_write_admissions(id uuid PRIMARY KEY,bucket_id uuid NOT NULL,kind text CHECK(kind IN ('proxy','multipart')),state text DEFAULT 'pending');
 CREATE TABLE object_storage_key_grants(bucket_id uuid,last_write_id uuid,reclaimable boolean);
 INSERT INTO object_upload_routes VALUES('00000000-0000-0000-0000-000000000001');
 INSERT INTO object_upload_completions VALUES('00000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','completed');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var phase string
	if err = pool.QueryRow(ctx, `SELECT write_phase FROM object_upload_completions`).Scan(&phase); err != nil || phase != "untracked" {
		t.Fatal("legacy receipt upgraded", phase, err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO object_upload_completions(id,route_id,status,write_phase) VALUES('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001','pending','prepared');
 INSERT INTO object_storage_write_admissions(id,bucket_id,kind,route_receipt) VALUES('00000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000004','proxy',true);
 INSERT INTO object_storage_key_grants VALUES('00000000-0000-0000-0000-000000000004','00000000-0000-0000-0000-000000000003',true);`)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`UPDATE object_upload_completions SET write_phase='secret-state'`,
		`UPDATE object_upload_completions SET status='failed' WHERE write_phase='prepared';UPDATE object_storage_write_admissions SET state='settled' WHERE route_receipt`,
		`UPDATE object_upload_completions SET recovery_token='lease'`,
	} {
		if _, err = pool.Exec(ctx, query); err == nil {
			t.Fatal("missing state guard", query)
		}
	}
	if _, err = pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback discarded pending writes")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM object_upload_routes`); err != nil {
		t.Fatal(err)
	}
	var oldPending int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_storage_write_admissions WHERE kind='proxy' AND state='pending'`).Scan(&oldPending); err != nil || oldPending != 1 {
		t.Fatal("older worker overlooks route writes", oldPending, err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM object_upload_completions WHERE route_id IS NULL`).Scan(&count); err != nil || count != 2 {
		t.Fatal("route deletion lost receipts", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET status='failed',write_phase='settled' WHERE write_phase='prepared';UPDATE object_storage_write_admissions SET state='settled' WHERE route_receipt`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	var reclaimable bool
	if err = pool.QueryRow(ctx, `SELECT reclaimable FROM object_storage_key_grants`).Scan(&reclaimable); err != nil || reclaimable {
		t.Fatal("rollback retained tracked grant", reclaimable, err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("down/up", err)
	}
}
