//go:build !no_pg

// adr: 521
package main

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationOperatorHTTPPostgres(t *testing.T) {
	if os.Getenv("GREGALE_OPERATIONS_ACCEPTANCE") != "1" {
		t.Skip("requires owned PostgreSQL qualification cluster")
	}
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	testOperationOperatorHTTP(t, state.NewPgStore(pool))
}
