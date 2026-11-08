//go:build !no_pg

package reconcile_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEnvironmentDeletionKeepsUnfinishedDeploymentSettings(t *testing.T) {
	testEnvironmentDeletionKeepsUnfinishedDeploymentSettings(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPgEnvironmentDeletionRacesDeploymentCreation(t *testing.T) {
	testEnvironmentDeletionRacesDeploymentCreation(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
