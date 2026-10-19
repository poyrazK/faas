//go:build !no_pg

// adr: 1344
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestNodeLeasePostgres(t *testing.T) {
	runNodeLeaseTests(t, func(t *testing.T) nodeLeaseTestStore {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	})
}
