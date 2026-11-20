package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1446
func TestDefaultRouteProbeSkipsFunctionsMemStore(t *testing.T) {
	checkDefaultRouteProbeSkipsFunctions(t, state.NewMemStore())
}

// adr: 1446
func TestDefaultRouteProbeSkipsFunctionsPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	checkDefaultRouteProbeSkipsFunctions(t, state.NewPgStore(pool))
}

// prod 2026-11-20: the default canary probe sent GET / to a queue function
// about 20 times a minute, and every path of a function is its handler, so
// the handler ran on synthetic events. A function is probed only on the paths
// it declares in release_probe_paths.
func checkDefaultRouteProbeSkipsFunctions(t *testing.T, s state.Store) {
	t.Helper()
	probes := s.(state.DefaultRouteProbeStore)
	ctx := t.Context()
	_, app, _, _ := healthFixtureOfType(t, s, state.AppTypeFunction)
	if targets, err := probes.ListDefaultRouteProbeTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("default probe targets for an undeclared function = %+v, %v; want none", targets, err)
	}
	manifest := app.Manifest
	manifest.ReleaseProbePaths = []string{"/health"}
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	targets, err := probes.ListDefaultRouteProbeTargets(ctx)
	if err != nil || len(targets) != 1 || len(targets[0].ProbePaths) != 1 || targets[0].ProbePaths[0] != "/health" {
		t.Fatalf("default probe targets for a function with release_probe_paths = %+v, %v; want one on /health", targets, err)
	}
}
