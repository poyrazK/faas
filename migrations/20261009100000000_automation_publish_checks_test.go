package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAutomationPublishChecksMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE apps(id uuid PRIMARY KEY); CREATE TABLE accounts(id uuid PRIMARY KEY);`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261009100000000_automation_publish_checks.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err := pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	if _, err := pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
