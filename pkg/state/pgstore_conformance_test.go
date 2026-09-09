//go:build !no_pg

package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/conformance"
)

func TestPgStoreConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) state.Store {
		// OpenMigrated only migrates when FAAS_PGTEST_TEMPLATE_DATABASE is
		// set (CI sets it for this shard); otherwise it falls back to
		// pgtest.Open, which hands back an EMPTY schema despite the name.
		// Without the MigrateUp below, a developer running this suite
		// locally gets `relation "accounts" does not exist` from the very
		// first Seed call rather than a contract failure.
		//
		// MigrateUp is idempotent, so it is a cheap no-op on the template
		// path and the thing that makes the suite runnable anywhere.
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(context.Background(), pool); err != nil {
			t.Fatalf("MigrateUp: %v", err)
		}
		return state.NewPgStore(pool)
	})
}
