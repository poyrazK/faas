//go:build !no_pg

package state

// adr: 1547. PostgreSQL retains both protocols' physical claims independently of ledgers.

import "testing"

func TestFunctionMigrationSharedCapacityPg(t *testing.T) {
	t.Run("independent reservations", func(t *testing.T) {
		s, _ := runtimeCapturePGStore(t)
		functionMigrationSharedCapacity(t, s)
	})
	t.Run("committed source cleanup", func(t *testing.T) {
		s, _ := runtimeCapturePGStore(t)
		functionMigrationCommittedSourceCapacity(t, s)
	})
}
