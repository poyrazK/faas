//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestHostingCICancellationMigration(t *testing.T) {
	// adr: 202610171242 — legacy identities remain usable; canceled identities cannot be forgotten on downgrade.
	pool := pgtest.Open(t)
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261017124228606_hosting_ci_cancellation.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, `CREATE TABLE hosting_ci_runs(id integer PRIMARY KEY);INSERT INTO hosting_ci_runs VALUES(1);`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var canceled bool
	if err = pool.QueryRow(ctx, `SELECT canceled FROM hosting_ci_runs WHERE id=1`).Scan(&canceled); err != nil || canceled {
		t.Fatal("legacy backfill", canceled, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE hosting_ci_runs SET canceled=true`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade lost cancellation")
	}
	if _, err = pool.Exec(ctx, `DELETE FROM hosting_ci_runs`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("drained downgrade", err)
	}
}
