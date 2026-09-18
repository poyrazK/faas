//go:build !no_pg

package migrations_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrationAppTCPListeners(t *testing.T) {
	pool := pgtest.Open(t)
	migrateUpOnce(t.Context(), t, pool)

	var tableCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from information_schema.tables
		 where table_schema = current_schema() and table_name = 'app_tcp_listeners'`).Scan(&tableCount); err != nil {
		t.Fatalf("query table: %v", err)
	}
	if tableCount != 1 {
		t.Fatalf("table count=%d, want 1", tableCount)
	}
	var indexCount int
	if err := pool.QueryRow(t.Context(), `
		select count(*) from pg_indexes
		 where schemaname = current_schema()
		   and tablename = 'app_tcp_listeners'
		   and indexname like 'app_tcp_listeners_%'
		   and indexname <> 'app_tcp_listeners_pkey'`).Scan(&indexCount); err != nil {
		t.Fatalf("query indexes: %v", err)
	}
	if indexCount != 4 {
		t.Fatalf("index count=%d, want 4", indexCount)
	}
}
