package migrations_test

import (
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"strings"
	"testing"
)

func TestAutomationRevisionCheckEvidenceMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	_, err := pool.Exec(ctx, `CREATE TABLE workflow_automation_revisions(version bigint PRIMARY KEY); INSERT INTO workflow_automation_revisions VALUES(1);`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261010071620872_automation_revision_check_evidence.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err = pool.Exec(ctx, parts[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	var absent bool
	if err = pool.QueryRow(ctx, `SELECT check_evidence IS NULL FROM workflow_automation_revisions WHERE version=1`).Scan(&absent); err != nil || !absent {
		t.Fatalf("legacy evidence invented: %v/%v", absent, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO workflow_automation_revisions(version,check_evidence) VALUES(2,'{"checked_version":1}');`); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM workflow_automation_revisions WHERE check_evidence IS NULL`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("rollback/reapply lost revisions: %d/%v", count, err)
	}
}
