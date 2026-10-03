//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestIssueHandoffMigrationReplayPreservesOutbox(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	var version int64
	for _, m := range migrations.LoadMigrations(t) {
		if strings.HasSuffix(m.Name, "_issue_evidence_handoffs.sql") {
			version = m.Version
		}
	}
	if version == 0 {
		t.Fatal("handoff migration missing")
	}
	account, app, source := uuid.NewString(), uuid.NewString(), uuid.NewString()
	routeReplayExec(t, pool, `INSERT INTO accounts(id,email,plan) VALUES($1,$2,'hobby')`, account, account+"@example.com")
	routeReplayExec(t, pool, `INSERT INTO apps(id,account_id,slug,ram_mb) VALUES($1,$2,$3,128)`, app, account, "handoff-"+app[:8])
	routeReplayExec(t, pool, `INSERT INTO app_webhook_event_outbox(account_id,app_id,event,source_id,payload,recipient_webhook_ids)
	 VALUES($1,$2,'issue.handoff',$3,'{"schema_version":1}',ARRAY[$4::uuid])`, account, app, source, uuid.NewString())
	routeReplayExec(t, pool, `DELETE FROM goose_db_version WHERE version_id=$1`, version)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatalf("replay with retained handoff: %v", err)
	}
	var payload string
	if err := pool.QueryRow(t.Context(), `SELECT payload::text FROM app_webhook_event_outbox WHERE event='issue.handoff' AND source_id=$1`, source).Scan(&payload); err != nil || payload != `{"schema_version": 1}` {
		t.Fatalf("handoff changed after replay: %q %v", payload, err)
	}
}
