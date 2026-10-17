// adr: 1289
package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAutomationTestSuitesMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `CREATE TABLE workflow_automation_definitions(name text PRIMARY KEY,draft jsonb); INSERT INTO workflow_automation_definitions VALUES('existing','{"name":"existing"}')`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261015000000001_automation_test_suites.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err := pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	var empty bool
	if err := pool.QueryRow(ctx, `SELECT test_suite IS NULL AND test_report IS NULL FROM workflow_automation_definitions WHERE name='existing'`).Scan(&empty); err != nil || !empty {
		t.Fatal("existing draft changed", err)
	}
	for _, column := range []string{"test_suite", "test_report"} {
		if _, err := pool.Exec(ctx, `UPDATE workflow_automation_definitions SET `+column+`='[]'`); err == nil {
			t.Fatal("invalid shape accepted", column)
		}
	}
	if _, err := pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("reapply", err)
	}
}
