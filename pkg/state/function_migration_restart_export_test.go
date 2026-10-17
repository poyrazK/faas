//go:build !no_pg

package state

// adr: 1665

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// FunctionMigrationRestartFixtureForTest exposes only a disposable catalog's
// measured readiness fixture to the external scheduler/store integration test.
// Native consumption is simulated; it never supplies native evidence.
func FunctionMigrationRestartFixtureForTest(t *testing.T) (*pgxpool.Pool, Instance, FunctionMigrationDestination) {
	t.Helper()
	s, pool := runtimeCapturePGStore(t)
	ins, g, r, _ := functionDestinationFixture(t, s)
	if err := s.ReadyFunctionMigrationDestination(t.Context(), g, r); err != nil {
		t.Fatal(err)
	}
	return pool, ins, FunctionMigrationDestination{Grant: g, Receipt: &r, Phase: FunctionDestinationReady}
}
