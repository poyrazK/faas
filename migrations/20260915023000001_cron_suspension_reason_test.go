//go:build !no_pg

package migrations_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationCronSuspensionReason(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var nullable, defaultValue string
	if err := pool.QueryRow(t.Context(), `
		select is_nullable, column_default
		  from information_schema.columns
		 where table_schema = current_schema()
		   and table_name = 'crons'
		   and column_name = 'suspended_reason'
	`).Scan(&nullable, &defaultValue); err != nil {
		t.Fatalf("load crons.suspended_reason: %v", err)
	}
	if nullable != "NO" || !strings.Contains(defaultValue, "''") {
		t.Fatalf("crons.suspended_reason nullable=%q default=%q, want NOT NULL default empty", nullable, defaultValue)
	}

	var definition string
	if err := pool.QueryRow(t.Context(), `
		select pg_get_constraintdef(oid)
		  from pg_constraint
		 where conrelid = 'crons'::regclass
		   and conname = 'crons_suspended_reason_chk'
	`).Scan(&definition); err != nil {
		t.Fatalf("load crons_suspended_reason_chk: %v", err)
	}
	for _, value := range []string{"''", "'no_live_deployment'"} {
		if !strings.Contains(definition, value) {
			t.Fatalf("crons_suspended_reason_chk missing %s: %s", value, definition)
		}
	}
}
