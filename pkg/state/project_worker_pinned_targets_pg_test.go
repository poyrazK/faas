//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerPinnedTargetsPG(t *testing.T) {
	testProjectWorkerPinnedTargets(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
