//go:build !no_pg

package migrations_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// triggerWhenMigration splits the revision triggers and gives them WHEN
// clauses on their watched columns. A test that replays an older migration
// touching those triggers or columns rolls it back first and reapplies it
// after, as an ordinary down/up chain would.
const triggerWhenMigration = "20261102193333680_trigger_when_unchanged_columns.sql"

// execTriggerWhen runs triggerWhenMigration's Down (down) or Up section.
func execTriggerWhen(t *testing.T, ctx context.Context, pool *pgxpool.Pool, down bool) {
	t.Helper()
	raw, err := os.ReadFile(triggerWhenMigration)
	if err != nil {
		t.Fatal(err)
	}
	up, rollback, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("trigger WHEN migration has no Down")
	}
	sql := up
	if down {
		sql = rollback
	}
	if _, err := pool.Exec(ctx, sql); err != nil {
		t.Fatalf("%s (down=%t): %v", triggerWhenMigration, down, err)
	}
}

// TestRevisionTriggersSkipUnchangedColumns pins the WHEN clauses that keep
// capture_binding_promotion_revision from running on updates that leave its
// watched columns unchanged: every row trigger that calls it on UPDATE carries
// a WHEN comparing each watched column, so a trigger added later without one
// fails here.
func TestRevisionTriggersSkipUnchangedColumns(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	defer pool.Close()
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(ctx, `
		SELECT c.relname, t.tgname, t.tgargs, pg_get_triggerdef(t.oid)
		  FROM pg_trigger t
		  JOIN pg_class c ON c.oid = t.tgrelid
		  JOIN pg_namespace n ON n.oid = c.relnamespace
		  JOIN pg_proc p ON p.oid = t.tgfoid
		 WHERE n.nspname = current_schema() AND NOT t.tgisinternal
		   AND p.proname = 'capture_binding_promotion_revision'
		   AND t.tgtype & 16 <> 0
		 ORDER BY c.relname, t.tgname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	updates := 0
	for rows.Next() {
		var table, name, def string
		var raw []byte
		if err := rows.Scan(&table, &name, &raw, &def); err != nil {
			t.Fatal(err)
		}
		updates++
		columns := strings.Split(strings.TrimRight(string(raw), "\x00"), ",")
		when, ok := strings.CutPrefix(def[strings.Index(def, " WHEN ")+1:], "WHEN ")
		if !strings.Contains(def, " WHEN ") || !ok {
			t.Errorf("%s.%s runs on every UPDATE; add a WHEN on its watched columns", table, name)
			continue
		}
		for _, column := range columns {
			if !strings.Contains(when, "old."+column) || !strings.Contains(when, "new."+column) {
				t.Errorf("%s.%s WHEN does not compare %s: %s", table, name, column, when)
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if updates != 23 {
		t.Fatalf("revision update triggers = %d, want 23", updates)
	}
}
