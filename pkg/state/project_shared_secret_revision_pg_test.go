//go:build !no_pg

// adr: 20261104201200000
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgSharedSecretQualificationRevisions(t *testing.T) {
	for _, mutation := range []string{"add", "rotate", "delete", "recreate"} {
		t.Run(mutation, func(t *testing.T) {
			testSharedSecretQualificationRevisions(t, state.NewPgStore(pgtest.OpenMigrated(t)), mutation)
		})
	}
}
