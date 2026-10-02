//go:build !no_pg

// adr: 438
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOrdinaryPlacementPreservesRegionalRecoveryPostgres(t *testing.T) {
	exerciseOrdinaryRegionalPlacement(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
