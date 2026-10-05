//go:build !no_pg

// adr: 594
package state

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestJobExecutionInstancesPostgresRetainsTerminalPlacement(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	exerciseJobExecutionAudit(t, NewPgStore(pool))
}
