//go:build !no_pg

// adr: 437
package sched

import (
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestRegionalServiceCapacityPlacementPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseRegionalServiceCapacityPlacement(t, state.NewPgStore(pool))
}
