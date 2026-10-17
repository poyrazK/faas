package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessOAuthMigrationReplay(t *testing.T) {
	// Always migrate an isolated schema, even when the template database
	// harness is enabled: cloning public would hide schema-qualified DDL bugs.
	pool := pgtest.Open(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181314940_mcp_readiness_oauth.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	// Replays must preserve future function definitions.
	if _, err = tx.Exec(ctx, `CREATE OR REPLACE FUNCTION mcp_readiness_subject(d deployments) RETURNS text LANGUAGE sql STABLE AS 'SELECT ''future-definition'''`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_functiondef('mcp_readiness_subject(deployments)'::regprocedure)`).Scan(&definition); err != nil || !strings.Contains(definition, "future-definition") {
		t.Fatal("replay replaced later definition", err)
	}
}
