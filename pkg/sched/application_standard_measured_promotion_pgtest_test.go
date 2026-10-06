//go:build !no_pg

package sched

// adr: 595 Real PostgreSQL publication with simulated native acknowledgments.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardOrdinaryWarmMeasuredPromotion(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseApplicationStandardOrdinaryWarmMeasuredPromotion(t, state.NewPgStore(pool))
}
