//go:build !no_pg

// adr: 590
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrdinaryPlacementPreservesRegionalRecoveryPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseOrdinaryRegionalPlacement(t, state.NewPgStore(pool))
}
