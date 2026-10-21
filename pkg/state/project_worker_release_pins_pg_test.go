//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestProjectWorkerReleasePinsPG(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	testProjectWorkerReleasePins(t, state.NewPgStore(pool), func() state.Store { return state.NewPgStore(pool) }, func(id string) {
		if _, err := pool.Exec(t.Context(), "UPDATE project_release_sets SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1 AND NOT active", id); err != nil {
			t.Fatal(err)
		}
	})
}
