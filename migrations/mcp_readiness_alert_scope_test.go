package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessAlertScopeMigrationReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, e := pool.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, e := os.ReadFile("20261017181314996_mcp_readiness_alert_scope.sql")
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
		if _, e = tx.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = tx.Exec(ctx, `CREATE OR REPLACE FUNCTION guard_mcp_readiness_alert_scope() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN /* future-definition */ RETURN NEW; END;$$`); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(ctx, parts[0]); e != nil {
		t.Fatal(e)
	}
	var def string
	if e = tx.QueryRow(ctx, `SELECT pg_get_functiondef('guard_mcp_readiness_alert_scope()'::regprocedure)`).Scan(&def); e != nil || !strings.Contains(def, "future-definition") {
		t.Fatal("replay replaced subsequent guard", e)
	}
}
