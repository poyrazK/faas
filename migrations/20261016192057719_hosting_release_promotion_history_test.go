//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingReleasePromotionHistoryMigration(t *testing.T) {
	// adr: 202610161920 — preserve legacy ready visibility, reject unsafe downgrade.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261016192057719_hosting_release_promotion_history.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE hosting_releases(id integer,state text,created_at timestamptz NOT NULL DEFAULT clock_timestamp());
 INSERT INTO hosting_releases(id,state) VALUES (1,'ready'),(2,'pending'),(3,'retiring');`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var promoted int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM hosting_releases WHERE first_promoted_at=created_at`).Scan(&promoted); err != nil || promoted != 1 {
		t.Fatal("legacy backfill", promoted, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO hosting_releases(id,state) VALUES (4,'ready')`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET first_promoted_at=created_at+interval '1 second' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("ledger replay", err)
	}
	var preserved bool
	if err = pool.QueryRow(ctx, `SELECT (SELECT first_promoted_at IS NULL FROM hosting_releases WHERE id=4)
 AND (SELECT first_promoted_at=created_at+interval '1 second' FROM hosting_releases WHERE id=1)`).Scan(&preserved); err != nil || !preserved {
		t.Fatal("replay changed staged visibility or accepted promotion time", preserved, err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade exposed staged release")
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_releases SET first_promoted_at='infinity' WHERE id=4`); err == nil {
		t.Fatal("infinite promotion marker")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_releases WHERE id=4`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained rollback", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("forward replay", err)
	}
}
