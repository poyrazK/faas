//go:build !no_pg

package state

// adr: 20261021120000

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestProjectServiceRemovalsPG(t *testing.T) {
	testProjectServiceRemovals(t, func(t *testing.T) Store { return NewPgStore(pgtest.OpenMigrated(t)) })
}
