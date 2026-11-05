//go:build !no_pg

// adr: 117. PostgreSQL's deployment watchdog matches MemStore.
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgDeploymentWatchdog(t *testing.T) {
	testDeploymentWatchdog(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
