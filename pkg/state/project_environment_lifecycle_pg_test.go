//go:build !no_pg

// adr: 20261104110000
// adr: 20261104201200000
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectEnvironmentLifecycle(t *testing.T) {
	t.Run("clone membership", func(t *testing.T) { testEnvironmentCloneMembership(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
	t.Run("legacy clone admission", func(t *testing.T) { testEnvironmentLegacyCloneAdmission(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
	t.Run("cleanup reservation", func(t *testing.T) { testEnvironmentCleanupReservation(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
	t.Run("retired rollback", func(t *testing.T) { testEnvironmentRetiredRollback(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
	t.Run("rollback deletion race", func(t *testing.T) { testEnvironmentRollbackDeletionRace(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
	t.Run("last stage membership", func(t *testing.T) { testEnvironmentLastStageMembership(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
