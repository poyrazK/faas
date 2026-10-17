//go:build !no_pg

package state

// adr: 1578

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFunctionDestinationRestartPg(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(map[bool]string{false: "committed cleanup", true: "expired readiness"}[expired], func(t *testing.T) {
			s, pool := runtimeCapturePGStore(t)
			reopened, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(reopened.Close)
			functionDestinationRestartDiscovery(t, s, func() functionRestartTestStore { return NewPgStore(reopened) }, expired)
		})
	}
}

func TestFunctionDestinationRestartCapacityPg(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	reopened, err := pgxpool.NewWithConfig(t.Context(), pool.Config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	functionDestinationCapacity(t, s, func() functionDestinationTestStore { return NewPgStore(reopened) })
}
