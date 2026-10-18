package state_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1446
func TestDefaultRouteProbeStoreMemStore(t *testing.T) {
	checkDefaultRouteProbeStore(t, state.NewMemStore())
}

// adr: 1446
func TestDefaultRouteProbeStorePostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	checkDefaultRouteProbeStore(t, state.NewPgStore(pool))
}

// checkDefaultRouteProbeStore: a safe-policy canary with one serving
// predecessor is a default probe target until the app opts into immediate
// releases, and probe totals sum each side over [since, until).
func checkDefaultRouteProbeStore(t *testing.T, s state.Store) {
	t.Helper()
	probes, ok := s.(state.DefaultRouteProbeStore)
	if !ok {
		t.Fatalf("%T does not implement state.DefaultRouteProbeStore", s)
	}
	ctx := t.Context()
	account, app, stable, candidate := healthFixture(t, s)
	targets, err := probes.ListDefaultRouteProbeTargets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := state.RouteProbeTarget{AccountID: account.ID, AppID: app.ID, Slug: app.Slug, CandidateID: candidate.ID, StableID: stable.ID}
	if len(targets) != 1 || !reflect.DeepEqual(targets[0], want) {
		t.Fatalf("default probe targets = %+v, want [%+v]", targets, want)
	}

	minute := time.Now().UTC().Truncate(time.Minute)
	since := minute.Add(-3 * time.Minute)
	observations := []state.RouteProbeObservation{
		{DeploymentID: candidate.ID, Method: "GET", Path: "/", WindowStart: since.Add(-time.Minute), Requests: 10, ServerErrors: 10},
		{DeploymentID: candidate.ID, Method: "GET", Path: "/", WindowStart: since, Requests: 10, ServerErrors: 2, Unauthenticated: 1},
		{DeploymentID: candidate.ID, Method: "GET", Path: "/reports/{id}", WindowStart: since.Add(time.Minute), Requests: 10},
		{DeploymentID: stable.ID, Method: "GET", Path: "/", WindowStart: since, Requests: 10},
		{DeploymentID: stable.ID, Method: "GET", Path: "/", WindowStart: minute, Requests: 10, ServerErrors: 10},
	}
	if err := s.(state.RouteProbeStore).RecordRouteProbeObservations(ctx, account.ID, app.ID, observations); err != nil {
		t.Fatal(err)
	}
	c, st, err := probes.RouteProbeTotals(ctx, app.ID, candidate.ID, stable.ID, since, minute)
	if err != nil {
		t.Fatal(err)
	}
	if want := (state.RouteProbeTotal{Requests: 20, ServerErrors: 2, Unauthenticated: 1}); c != want {
		t.Fatalf("candidate totals = %+v, want %+v", c, want)
	}
	if want := (state.RouteProbeTotal{Requests: 10}); st != want {
		t.Fatalf("stable totals = %+v, want %+v", st, want)
	}

	manifest := app.Manifest
	manifest.ReleasePolicy = api.ReleasePolicyImmediate
	if _, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	if targets, err := probes.ListDefaultRouteProbeTargets(ctx); err != nil || len(targets) != 0 {
		t.Fatalf("default probe targets after release_policy=immediate = %+v, %v; want none", targets, err)
	}
}
