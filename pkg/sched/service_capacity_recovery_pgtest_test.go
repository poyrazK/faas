//go:build !no_pg

// adr: 422
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProtectedServiceRecoveryPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	exerciseProtectedServiceRecovery(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) })
}

func TestProtectedServiceRecoveryMixedSlotsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	exerciseProtectedServiceMixedSlots(t, state.NewPgStore(pool))
}
