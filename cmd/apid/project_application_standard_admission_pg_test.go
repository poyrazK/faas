//go:build !no_pg

package main

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgProjectApplicationStandardsOnboarding(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	standardProjectOnboarding(t, state.NewPgStore(pool))
}

func TestPgProjectApplicationStandardsInterruptedInstallation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	standardProjectInterruptedInstallation(t, state.NewPgStore(pool))
}

func TestPgProjectApplicationStandardsBlockedInstallation(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	standardProjectBlockedInstallation(t, state.NewPgStore(pool))
}

func TestPgProjectApplicationStandardsScopeChange(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	standardProjectScopeChange(t, state.NewPgStore(pool))
}
