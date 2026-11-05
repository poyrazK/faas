//go:build !no_pg

// adr: 094. PostgreSQL releases a torn-down preview's slug like MemStore.
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgPreviewTombstoneReleasesSlug(t *testing.T) {
	testPreviewTombstoneReleasesSlug(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
