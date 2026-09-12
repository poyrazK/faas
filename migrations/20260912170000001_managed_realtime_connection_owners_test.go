//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationManagedRealtimeConnectionOwners(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var tableCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from information_schema.tables
		 where table_schema = current_schema()
		   and table_name = 'managed_realtime_connection_owners'`).Scan(&tableCount); err != nil {
		t.Fatalf("query owner table: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("owner table count=%d, want 1", tableCount)
	}
	var indexCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'managed_realtime_connection_owners'
		   and indexname like 'managed_realtime_connection_owners_%_idx'`).Scan(&indexCount); err != nil {
		t.Fatalf("query owner indexes: %v", err)
	}
	if indexCount != 2 {
		t.Fatalf("owner index count=%d, want 2", indexCount)
	}
}
