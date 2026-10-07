//go:build !no_pg

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgStandardLogInventoryQuietService(t *testing.T) {
	standardLogInventoryGatewayQuiet(t, inventoryGatewayPGStore(t))
}

func TestPgStandardLogInventoryRemovalWaitsForWorkers(t *testing.T) {
	standardLogInventoryGatewayRemoval(t, inventoryGatewayPGStore(t))
}

func inventoryGatewayPGStore(t *testing.T) *state.PgStore {
	t.Helper()
	p := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(p)
}
