//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingStorageAccountingMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261016112658296_hosting_account_storage_reservations.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err := pool.Exec(ctx, `CREATE TABLE hosting_releases(id integer, state text, manifest jsonb);
 INSERT INTO hosting_releases VALUES
 (1,'pending','{"files":[{"size":18},{"size":7}]}'),
 (2,'ready','{"files":[{"size":0}]}'),
 (3,'retiring','{"files":[{"size":12}]}');`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := pool.QueryRow(ctx, `SELECT sum(artifact_bytes) FROM hosting_releases`).Scan(&total); err != nil || total != 37 {
		t.Fatalf("backfill: %d %v", total, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO hosting_releases(id,state,manifest) VALUES (4,'pending','{"files":[]}')`); err == nil {
		t.Fatal("old writer admitted unaccounted release")
	}
	if _, err := pool.Exec(ctx, `UPDATE hosting_releases SET artifact_bytes=-1`); err == nil {
		t.Fatal("negative accounting accepted")
	}
	if _, err := pool.Exec(ctx, sections[1]); err == nil {
		t.Fatal("rollback discarded live accounting")
	}
	if _, err := pool.Exec(ctx, `DELETE FROM hosting_releases`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("drained down/up", err)
	}
}
