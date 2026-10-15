//go:build !no_pg

package state

// adr: 595 New serving-parent capture authority uses real PostgreSQL transactions.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPostgresStandardServingRestoreCapture(t *testing.T) {
	standardServingRestoreCaptureCases(t, func(t *testing.T) standardSnapshotPublicationTestStore {
		return NewPgStore(pgtest.OpenMigrated(t))
	})
}
