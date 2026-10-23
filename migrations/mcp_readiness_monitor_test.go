//go:build !no_pg

package migrations_test

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"os"
	"strings"
	"testing"
)

func TestMCPReadinessMonitorMigrations(t *testing.T) {
	ctx := t.Context()
	// A ledger replay of either Up on the complete schema is a no-op.
	full := openMigrated(t)
	for _, file := range []string{"20261017181314907_mcp_readiness_monitor.sql", "20261017181314925_mcp_readiness_monitor_alert.sql"} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		up, _, _ := strings.Cut(string(raw), "-- +goose Down")
		if _, err := full.Exec(ctx, up); err != nil {
			t.Fatalf("replay %s: %v", file, err)
		}
	}
	// Down is exercised at this migration's own point in history: on the
	// complete schema it would restore a metric check that later presets
	// (route_insights_detected, 20261020161647152) no longer satisfy, and goose
	// only ever runs it after rolling those later migrations back.
	pool := pgtest.Open(t)
	migrateUpTo(t, ctx, pool, 20261017181314925)
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
