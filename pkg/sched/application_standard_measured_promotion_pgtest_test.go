//go:build !no_pg

package sched_test

// adr: 595 Real PostgreSQL publication with simulated native acknowledgments.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardOrdinaryWarmMeasuredPromotion(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	sched.ExerciseApplicationStandardOrdinaryWarmMeasuredPromotion(t, state.NewPgStore(pool))
}
