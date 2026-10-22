//go:build !no_pg

// adr: 20261017221000
package sched_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkerRecoveryPostgresRestartWithoutEvents(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	sched.ExerciseWorkerRecoveryRestart(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) })
}
