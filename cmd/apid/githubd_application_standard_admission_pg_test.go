//go:build !no_pg

// adr: 595. First GitHub build admission must match PostgreSQL authority.
package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgGithubApplicationStandardsDeployment(t *testing.T) {
	standardGithubDeploymentTests(t, func(t *testing.T) standardProjectBoundaryStore {
		pool := pgtest.OpenMigrated(t)
		if err := db.MigrateUp(t.Context(), pool); err != nil {
			t.Fatal(err)
		}
		return state.NewPgStore(pool)
	})
}
