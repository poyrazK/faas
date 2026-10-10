//go:build !no_pg

// adr: 905
package migrations_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestAutomationPauseNotificationVocabularyPreservesEventsAndReplay(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := t.Context()
	raw, err := migrations.FS.ReadFile("20261010071421609_automation_pause_notification_vocabulary.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("missing down migration")
	}
	tables := []string{"app_webhook_event_outbox", "app_webhook_deliveries"}
	for _, table := range tables[:1] {
		// Simulate a later release's vocabulary, including an event unknown
		// to the automation feature. The fixture contains no ledger entries.
		ddl := fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s_event_chk; ALTER TABLE %s ADD CONSTRAINT %s_event_chk CHECK (event IN ('future.event','workflow.finished','event_recovery.execution_finished'))", table, table, table, table)
		if _, err := pool.Exec(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	definitions := make(map[string]string)
	for pass := 0; pass < 3; pass++ {
		sql := up
		if pass == 2 {
			sql = down
		}
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		for _, table := range tables {
			var definition string
			if err := pool.QueryRow(ctx, "SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2", table, table+"_event_chk").Scan(&definition); err != nil {
				t.Fatal(err)
			}
			if pass > 0 && definition != definitions[table] {
				t.Fatalf("%s replay or rollback changed the vocabulary", table)
			}
			definitions[table] = definition
			expression := strings.TrimSuffix(strings.TrimPrefix(definition, "CHECK ("), ")")
			for _, event := range []string{"future.event", "workflow.finished", "event_recovery.execution_finished", "automation.paused", "unsupported.event"} {
				var allowed bool
				if err := pool.QueryRow(ctx, "SELECT "+expression+" FROM (SELECT $1::text AS event) AS candidate", event).Scan(&allowed); err != nil {
					t.Fatal(err)
				}
				if allowed != (event != "unsupported.event" || table == "app_webhook_deliveries") {
					t.Fatalf("%s: event %q allowed=%v", table, event, allowed)
				}
			}
		}
	}
}
