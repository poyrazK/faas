//go:build !no_pg

package promotion_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgRollbackWorkloadRestoresRetainedTraffic(t *testing.T) {
	testRollbackWorkloadRestoresTraffic(t, state.NewPgStore(pgtest.OpenMigrated(t)), 3600)
}

func TestPgRollbackWorkloadRestoresUnretainedRevision(t *testing.T) {
	testRollbackWorkloadRestoresTraffic(t, state.NewPgStore(pgtest.OpenMigrated(t)), 0)
}

func TestPgRollbackWorkloadClearsDarkTarget(t *testing.T) {
	testRollbackWorkloadClearsDarkTarget(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
