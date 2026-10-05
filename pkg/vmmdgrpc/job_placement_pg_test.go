//go:build !no_pg

// ADR-594: the receiving vmmd corroborates real PostgreSQL job claims and
// rejects a cancelled claim without allocating a second VM.
package vmmdgrpc_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestJobColdBoot_PostgresClaimSurvivesAdmissionPause(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseDurableJobClaim(t, state.NewPgStore(pool))
}
