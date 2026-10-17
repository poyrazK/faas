package migrations_test

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"os"
	"strings"
	"testing"
)

func TestMCPReadinessIncidentTimelineMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315007_mcp_readiness_incident_timeline.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	var def string
	if err = tx.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE indexname='alert_deliveries_mcp_readiness_timeline_idx'`).Scan(&def); err != nil || !strings.Contains(def, "incident_id") {
		t.Fatal(def, err)
	}
}
