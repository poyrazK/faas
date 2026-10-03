//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// openMigratedRegionalPool also migrates the fresh-schema fallback used when
// the optional template database harness is disabled.
func openMigratedRegionalPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatalf("migrate regional PostgreSQL fixture: %v", err)
	}
	return pool
}
