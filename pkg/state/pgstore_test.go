package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgStore stands up a fresh schema, migrates it, and returns a PgStore. Skips
// when Postgres is unreachable (pgtest.Open handles the skip). These round-trips
// lock the hand-written SQL in pgstore.go against a real cluster (ADR-017) —
// especially the schedd wake-path methods added for M5.
func pgStore(t *testing.T) (*state.PgStore, context.Context) {
	t.Helper()
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return state.NewPgStore(pool), ctx
}

// seedLiveDeploy creates account+app+live-deployment and returns their ids.
func seedLiveDeploy(t *testing.T, s *state.PgStore, ctx context.Context) (acctID, appID, depID string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "u@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "pg-app", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	return acct.ID, app.ID, dep.ID
}

func TestPg_SetInstanceRuntimeAndRunningLookup(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	// No RUNNING instance yet.
	if _, err := s.RunningInstanceForApp(ctx, appID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("RunningInstanceForApp before runtime = %v, want ErrNotFound", err)
	}

	if err := s.SetInstanceRuntime(ctx, ins.ID, "fc-"+ins.ID, "10.100.0.5", 20005); err != nil {
		t.Fatalf("SetInstanceRuntime: %v", err)
	}
	if err := s.UpdateInstanceState(ctx, ins.ID, string(state.StateRunning)); err != nil {
		t.Fatalf("UpdateInstanceState: %v", err)
	}

	got, err := s.RunningInstanceForApp(ctx, appID)
	if err != nil {
		t.Fatalf("RunningInstanceForApp: %v", err)
	}
	if got.ID != ins.ID || got.HostIP != "10.100.0.5" || got.GuestUID != 20005 || got.Netns != "fc-"+ins.ID {
		t.Errorf("instance runtime round-trip = %+v", got)
	}
	if got.StartedAt.IsZero() {
		t.Error("started_at should be stamped by SetInstanceRuntime")
	}
}

func TestPg_TouchInstancesLastSeen(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	ins, _ := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512)

	when := time.Now().Add(-30 * time.Second).Truncate(time.Millisecond)
	applied, err := s.TouchInstancesLastSeen(ctx, []state.InstanceTouch{
		{InstanceID: ins.ID, LastRequest: when},
		{InstanceID: "00000000-0000-0000-0000-000000000000", LastRequest: when}, // unknown → dropped
	})
	if err != nil {
		t.Fatalf("TouchInstancesLastSeen: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1", applied)
	}
	got, _ := s.InstanceByID(ctx, ins.ID)
	if !got.LastRequestAt.Equal(when) {
		t.Errorf("last_request_at = %v, want %v", got.LastRequestAt, when)
	}

	// Empty batch is a no-op, not an error.
	if n, err := s.TouchInstancesLastSeen(ctx, nil); err != nil || n != 0 {
		t.Errorf("empty batch = (%d, %v), want (0, nil)", n, err)
	}
}

func TestPg_MarkSnapshotStale(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.10.0", Path: "/srv/fc/snap/x", MemBytes: 1,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if _, err := s.LatestSnapshot(ctx, depID); err != nil {
		t.Fatalf("LatestSnapshot before stale: %v", err)
	}
	if err := s.MarkSnapshotStale(ctx, snap.ID); err != nil {
		t.Fatalf("MarkSnapshotStale: %v", err)
	}
	if _, err := s.LatestSnapshot(ctx, depID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("LatestSnapshot after stale = %v, want ErrNotFound", err)
	}
}

func TestPg_LiveDeploymentAndListAllApps(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)

	dep, err := s.LiveDeployment(ctx, appID)
	if err != nil {
		t.Fatalf("LiveDeployment: %v", err)
	}
	if dep.ID != depID {
		t.Errorf("live deployment = %q, want %q", dep.ID, depID)
	}

	apps, err := s.ListAllApps(ctx)
	if err != nil {
		t.Fatalf("ListAllApps: %v", err)
	}
	if len(apps) != 1 || apps[0].ID != appID {
		t.Errorf("ListAllApps = %+v, want one app %q", apps, appID)
	}
}

// TestPg_SetAppMinInstances_RoundTrip mirrors TestSetAppMinInstances_RoundTrip
// in app_min_instances_test.go to lock PgStore parity for ux_spec §6.5.
// The MemStore test catches the API shape; this test catches the SQL.
func TestPg_SetAppMinInstances_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	// Default reads as 0 (scale to zero).
	got, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.MinInstances != 0 {
		t.Errorf("default MinInstances = %d, want 0", got.MinInstances)
	}

	// Set 2 → re-read → 2.
	if err := s.SetAppMinInstances(ctx, appID, 2); err != nil {
		t.Fatalf("SetAppMinInstances(2): %v", err)
	}
	got, err = s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.MinInstances != 2 {
		t.Errorf("after Set 2: MinInstances = %d, want 2", got.MinInstances)
	}

	// Reset to 0.
	if err := s.SetAppMinInstances(ctx, appID, 0); err != nil {
		t.Fatalf("SetAppMinInstances(0): %v", err)
	}
	got, err = s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.MinInstances != 0 {
		t.Errorf("after Set 0: MinInstances = %d, want 0", got.MinInstances)
	}

	// Unknown app → ErrNotFound (covers the RowsAffected==0 branch).
	if err := s.SetAppMinInstances(ctx, "00000000-0000-0000-0000-000000000000", 1); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown app: err = %v, want ErrNotFound", err)
	}
}

// TestPg_UpdateApp_WithMinInstances pins the partial-update semantics
// of UpdateAppParams.MinInstances + SetMinInstances on PgStore. Mirrors
// the MemStore case at app_min_instances_test.go.
func TestPg_UpdateApp_WithMinInstances(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	// Pre-set floor 2 so "unset" must leave it alone.
	if err := s.SetAppMinInstances(ctx, appID, 2); err != nil {
		t.Fatalf("seed set: %v", err)
	}

	// PATCH with no MinInstances field → column stays at 2.
	a, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{})
	if err != nil {
		t.Fatalf("UpdateApp unset: %v", err)
	}
	if a.MinInstances != 2 {
		t.Errorf("unset MinInstances: got %d, want 2 (must be unchanged)", a.MinInstances)
	}

	// PATCH explicit zero → 0.
	zero := 0
	a, err = s.UpdateApp(ctx, appID, state.UpdateAppParams{
		MinInstances: &zero, SetMinInstances: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp zero: %v", err)
	}
	if a.MinInstances != 0 {
		t.Errorf("explicit zero: got %d, want 0", a.MinInstances)
	}

	// PATCH 3 → 3.
	three := 3
	a, err = s.UpdateApp(ctx, appID, state.UpdateAppParams{
		MinInstances: &three, SetMinInstances: true,
	})
	if err != nil {
		t.Fatalf("UpdateApp three: %v", err)
	}
	if a.MinInstances != 3 {
		t.Errorf("explicit 3: got %d, want 3", a.MinInstances)
	}
}

// TestPg_ListLatestInstancePerApp pins the dashboard N+1 fix (PR #48
// follow-up): DISTINCT ON (app_id) returns exactly one row per app
// (the newest by started_at DESC) and the map is keyed by app ID.
func TestPg_ListLatestInstancePerApp(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, depID := seedLiveDeploy(t, s, ctx)

	// Empty before any instances exist → empty map.
	got, err := s.ListLatestInstancePerApp(ctx, acctID)
	if err != nil {
		t.Fatalf("empty: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("empty: got %v, want empty map", got)
	}

	// Create two instances; the second started later should win.
	old, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256)
	if err != nil {
		t.Fatalf("CreateInstance old: %v", err)
	}
	if err := s.SetInstanceRuntime(ctx, old.ID, "fc-"+old.ID, "10.100.0.5", 20005); err != nil {
		t.Fatalf("SetInstanceRuntime old: %v", err)
	}

	// Sleep briefly so the second instance has a strictly-later started_at.
	time.Sleep(10 * time.Millisecond)

	newer, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256)
	if err != nil {
		t.Fatalf("CreateInstance newer: %v", err)
	}
	if err := s.SetInstanceRuntime(ctx, newer.ID, "fc-"+newer.ID, "10.100.0.6", 20006); err != nil {
		t.Fatalf("SetInstanceRuntime newer: %v", err)
	}

	got, err = s.ListLatestInstancePerApp(ctx, acctID)
	if err != nil {
		t.Fatalf("ListLatestInstancePerApp: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 (one app, two instances collapse to newest)", len(got))
	}
	ins, ok := got[appID]
	if !ok {
		t.Fatalf("no entry for app %q in map %v", appID, got)
	}
	if ins.ID != newer.ID {
		t.Errorf("latest = %q, want %q (the newer of two)", ins.ID, newer.ID)
	}
}
