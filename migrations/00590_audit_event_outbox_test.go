//go:build !no_pg

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigration_00590_AuditEventOutbox(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	defer pool.Close()
	migrateUpOnce(ctx, t, pool)

	assertColumnShape(t, ctx, pool, "audit_event_outbox", "id", "bigint", "NO")
	assertColumnShape(t, ctx, pool, "audit_event_outbox", "data", "jsonb", "NO")
	assertColumnShape(t, ctx, pool, "audit_event_outbox", "state", "text", "NO")
	assertColumnShape(t, ctx, pool, "audit_event_outbox", "available_at", "timestamp with time zone", "NO")
	assertColumnShape(t, ctx, pool, "audit_event_outbox", "lease_until", "timestamp with time zone", "YES")
	assertColumnShape(t, ctx, pool, "events", "outbox_id", "bigint", "YES")

	assertOutboxIndex(t, ctx, pool, "audit_event_outbox_claim_idx", "audit_event_outbox", "state")
	assertOutboxIndex(t, ctx, pool, "events_outbox_id_uniq", "events", "outbox_id")

	var uniqueExists, fkExists bool
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from pg_constraint
			 where conrelid = 'audit_event_outbox'::regclass
			   and conname = 'audit_event_outbox_dedupe_key_uniq'
			   and contype = 'u'
		)`).Scan(&uniqueExists); err != nil {
		t.Fatalf("query outbox dedupe constraint: %v", err)
	}
	if !uniqueExists {
		t.Fatal("audit_event_outbox dedupe unique constraint missing")
	}
	if err := pool.QueryRow(ctx, `
		select exists (
			select 1 from pg_constraint
			 where conrelid = 'events'::regclass
			   and confrelid = 'audit_event_outbox'::regclass
			   and contype = 'f'
		)`).Scan(&fkExists); err != nil {
		t.Fatalf("query events.outbox_id foreign key: %v", err)
	}
	if !fkExists {
		t.Fatal("events.outbox_id foreign key missing")
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay MigrateUp: %v", err)
	}
}

func assertOutboxIndex(t *testing.T, ctx context.Context, pool *pgxpool.Pool, indexName, table, column string) {
	t.Helper()
	var definition string
	if err := pool.QueryRow(ctx, `
		select pg_get_indexdef(i.indexrelid)
		  from pg_index i
		  join pg_class c on c.oid = i.indexrelid
		 where c.relname = $1
		   and i.indrelid = $2::regclass
	`, indexName, table).Scan(&definition); err != nil {
		t.Fatalf("index %s on %s missing: %v", indexName, table, err)
	}
	if !strings.Contains(strings.ToLower(definition), strings.ToLower(column)) {
		t.Errorf("index %s definition does not reference %s: %s", indexName, column, definition)
	}
}
