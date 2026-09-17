//go:build !no_pg

package migrations_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// The original 00126 migration added a NOTIFY trigger for a per-process
// admission cache. Central admission now consumes the shared row on every
// request, so the latest migration must retire that trigger and its function;
// otherwise each request produces redundant cluster-wide notification work.
func TestMigrations_RateLimitNotifyRetired(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}

	var triggers int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_trigger
		 where tgrelid = 'pg_ratelimit_counters'::regclass
		   and tgname = 'pg_ratelimit_counters_notify'`).Scan(&triggers); err != nil {
		t.Fatalf("inspect rate-limit notify trigger: %v", err)
	}
	if triggers != 0 {
		t.Fatalf("pg_ratelimit_counters_notify count=%d, want 0", triggers)
	}

	var functions int
	if err := pool.QueryRow(ctx, `
		select count(*) from pg_proc
		 where proname = 'notify_pg_ratelimit_counters'
		   and pronamespace = current_schema()::regnamespace`).Scan(&functions); err != nil {
		t.Fatalf("inspect rate-limit notify function: %v", err)
	}
	if functions != 0 {
		t.Fatalf("notify_pg_ratelimit_counters count=%d, want 0", functions)
	}

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("replay db.MigrateUp: %v", err)
	}
}
