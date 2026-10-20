//go:build !no_pg

package state_test

// adr: 595 New serving-parent capture authority uses real PostgreSQL transactions.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresStandardServingRestoreCapture(t *testing.T) {
	standardServingRestoreCaptureCases(t, func(t *testing.T) state.StandardSnapshotPublicationTestStore {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	})
}
