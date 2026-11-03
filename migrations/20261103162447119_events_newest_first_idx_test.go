//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestMigrations_EventsNewestFirstIdx pins ADR-20261103094121: the operator
// events list (ListAllEventsPaged) reads events newest first through
// events_at_id_idx, with and without filters, instead of sorting the table.
func TestMigrations_EventsNewestFirstIdx(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	var valid bool
	if err := pool.QueryRow(ctx, `
		select i.indisvalid from pg_index i
		  join pg_class c on c.oid = i.indexrelid
		  join pg_namespace n on n.oid = c.relnamespace
		 where n.nspname = current_schema() and c.relname = 'events_at_id_idx'
	`).Scan(&valid); err != nil || !valid {
		t.Fatalf("events_at_id_idx: valid=%v err=%v", valid, err)
	}

	if _, err := pool.Exec(ctx, `
		insert into events (actor, kind, data)
		select 'apid',
		       case when g % 3 = 0 then 'operator.action.view' else 'wake.boot_started' end,
		       jsonb_build_object('n', g)
		  from generate_series(1, 1000) g
	`); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := pool.Exec(ctx, `analyze events`); err != nil {
		t.Fatalf("analyze events: %v", err)
	}

	// The query ListAllEventsPaged sends, with the same catch-all filters.
	const list = `
		select id, at, actor, kind, subject, data from events
		 where ($1 = '' or actor = $1)
		   and ($2 = '' or starts_with(kind, $2))
		   and ($3 = '' or subject = $3::uuid)
		   and ($4 = '0001-01-01 00:00:00+00:00'::timestamptz or at >= $4)
		 order by at desc, id desc
		 limit $5`
	for _, prefix := range []string{"", "operator.action.view"} {
		plan, err := explainNoSeqScan(ctx, t, pool, list, "", prefix, "", time.Time{}, 5)
		if err != nil {
			t.Fatalf("explain (prefix %q): %v", prefix, err)
		}
		if !strings.Contains(plan, "events_at_id_idx") || strings.Contains(plan, "Sort") {
			t.Errorf("prefix %q: the list does not read events_at_id_idx in order:\n%s", prefix, plan)
		}
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay-safety: second MigrateUp failed: %v", err)
	}
}
