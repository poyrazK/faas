//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingHistoryMigrationPreservesSnapshotsAndRejectsDowngrade(t *testing.T) {
	// adr: 202610170902 — history is not fabricated or lost during migration/downgrade.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261017090210137_hosting_release_provenance_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE hosting_sites(id uuid PRIMARY KEY);CREATE TABLE hosting_releases(id uuid PRIMARY KEY);
 INSERT INTO hosting_sites VALUES('11111111-1111-4111-8111-111111111111');INSERT INTO hosting_releases SELECT id FROM hosting_sites;`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM hosting_promotion_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("fabricated old history", count, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET provenance='{"description":"supplied"}'`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade lost metadata")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_promotion_events(site_id,revision,previous_revision,release_id,actor_account_id) SELECT id,2,1,id,id FROM hosting_sites`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_releases`); err != nil {
		t.Fatal("history blocked release GC", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade lost history")
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_promotion_events SET previous_revision=10`); err == nil {
		t.Fatal("invalid accepted revision")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_sites`); err != nil {
		t.Fatal("history blocked site GC", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained downgrade", err)
	}
}
