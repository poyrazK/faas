package migrations_test

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"os"
	"strings"
	"testing"
)

func TestMCPReadinessMonitorMigrations(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, file := range []string{"20261017181314907_mcp_readiness_monitor.sql", "20261017181314925_mcp_readiness_monitor_alert.sql"} {
		raw, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		parts := strings.Split(string(raw), "-- +goose Down")
		for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
			if _, e = tx.Exec(ctx, sql); e != nil {
				t.Fatalf("%s: %v", file, e)
			}
		}
	}
}
