package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessMigrationReplayAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	raw, err := os.ReadFile("20261017181314891_mcp_readiness_gate.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("invalid migration")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("Up replay: %v", err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("Up after Down: %v", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("second replay: %v", err)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_trigger WHERE tgname='deployment_mcp_readiness_gate' AND tgrelid='deployments'::regclass`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("guard count=%d error=%v", count, err)
	}
}
