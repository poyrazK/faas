package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPProtocolAlertMigrationReplayAndRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("20261017181314876_mcp_protocol_alerts.sql")
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
	var base string
	if err = tx.QueryRow(ctx, `SELECT mcp_protocol_alert_metric_base()`).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("Up replay failed: %v", err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatalf("Down failed: %v", err)
	}
	var restored string
	if err = tx.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk'`).Scan(&restored); err != nil || restored != base {
		t.Fatal("rollback did not restore the saved allowed metrics", err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatalf("Up after Down failed: %v", err)
	}
	var definition string
	if err = tx.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	// A ledger replay must not erase constraints widened by a later release.
	if _, err = tx.Exec(ctx, `ALTER TABLE alert_rules DROP CONSTRAINT alert_rules_metric_chk`); err != nil {
		t.Fatal(err)
	}
	expression := strings.TrimSuffix(strings.TrimPrefix(definition, "CHECK ("), ")")
	if _, err = tx.Exec(ctx, `ALTER TABLE alert_rules ADD CONSTRAINT alert_rules_metric_chk CHECK ((`+expression+`) OR metric='future_mcp_fixture')`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='alert_rules'::regclass AND conname='alert_rules_metric_chk'`).Scan(&definition); err != nil || !strings.Contains(definition, "future_mcp_fixture") {
		t.Fatal("replay replaced a newer constraint", err)
	}
}
