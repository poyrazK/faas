package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestWorkflowAutomationRevisionsMigration(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := t.Context()
	if _, err := pool.Exec(ctx, `
CREATE TABLE apps(id uuid PRIMARY KEY, account_id uuid NOT NULL);
CREATE TABLE workflow_automation_definitions(
 app_id uuid NOT NULL, name text NOT NULL, version bigint NOT NULL,
 draft jsonb NOT NULL, published jsonb, published_version bigint NOT NULL,
 enabled boolean NOT NULL, updated_at timestamptz NOT NULL,
 PRIMARY KEY(app_id,name)
);
INSERT INTO apps VALUES('00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002');
INSERT INTO workflow_automation_definitions VALUES(
 '00000000-0000-0000-0000-000000000001','paid',7,
 '{"name":"paid","steps":[]}','{"name":"paid","steps":[{"name":"send","path":"/v1"}]}',5,true,'2026-10-04T12:00:00Z'
),(
 '00000000-0000-0000-0000-000000000001','draft-only',3,
 '{"name":"draft-only","steps":[]}',NULL,0,true,'2026-10-04T12:00:00Z'
);`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261006062356953_workflow_automation_revisions.sql")
	if err != nil {
		t.Fatal(err)
	}
	sections := strings.SplitN(string(raw), "-- +goose Down", 2)
	for range 2 {
		if _, err = pool.Exec(ctx, sections[0]); err != nil {
			t.Fatal("apply/replay", err)
		}
	}
	var name, definition, actor string
	var version int64
	var legacy bool
	var keyIsNull bool
	if err := pool.QueryRow(ctx, `SELECT name,version,definition::text,legacy_snapshot,published_by_account_id::text,published_by_api_key_id IS NULL FROM workflow_automation_revisions`).Scan(&name, &version, &definition, &legacy, &actor, &keyIsNull); err != nil {
		t.Fatal(err)
	}
	if name != "paid" || version != 5 || !legacy || actor != "00000000-0000-0000-0000-000000000002" || !keyIsNull || !strings.Contains(definition, `"path": "/v1"`) {
		t.Fatalf("seeded revision=%s/%d legacy=%v actor=%s key_is_null=%v definition=%s", name, version, legacy, actor, keyIsNull, definition)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_automation_revisions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("backfilled rows=%d err=%v", count, err)
	}
	if _, err := pool.Exec(ctx, sections[1]); err != nil {
		t.Fatal("rollback", err)
	}
	if _, err := pool.Exec(ctx, sections[0]); err != nil {
		t.Fatal("reapply", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workflow_automation_revisions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("reapplied rows=%d err=%v", count, err)
	}
}
