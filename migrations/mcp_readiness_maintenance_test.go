package migrations_test

import (
	"os"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessMaintenanceMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	raw, err := os.ReadFile("20261017181315032_mcp_readiness_maintenance.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	for _, sql := range []string{parts[0], parts[0], parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal("migration replay", err)
		}
	}
	var account, app string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('maintenance-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'maintenance-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO mcp_readiness_maintenance_windows(id,account_id,app_id,scope,starts_at,ends_at,reason,created_by,created_at) VALUES(gen_random_uuid(),$1,$2,'staging',now(),now()+interval '1 hour','upgrade',$1,now())`, account, app); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal("live replay", err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded maintenance audit")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
}
