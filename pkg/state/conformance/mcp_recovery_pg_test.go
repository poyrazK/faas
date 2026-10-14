package conformance

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Exercise the same recovery contract on PostgreSQL when the light CI selector
// selects this package without selecting pkg/state's external conformance suite.
func TestPostgresMCPCompatibilityAndRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	testMCPCompatibilityAndRecovery(t, Seed(t, state.NewPgStore(pool)))
}
