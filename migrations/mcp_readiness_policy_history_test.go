package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessPolicyHistoryMigrationReplay(t *testing.T) {
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
	raw, e := os.ReadFile("20261017181314983_mcp_readiness_policy_history.sql")
	if e != nil {
		t.Fatal(e)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
		if _, e = tx.Exec(ctx, sql); e != nil {
			t.Fatal(e)
		}
	}
	var present bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND indexname='mcp_readiness_policy_history_page')`).Scan(&present); e != nil || !present {
		t.Fatal("history pagination index missing", e)
	}
}
