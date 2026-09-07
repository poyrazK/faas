//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestPgStoreConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) state.Store {
		return state.NewPgStore(pgtest.OpenMigrated(t))
	})
}
