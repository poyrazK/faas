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

// --- RenameApp (issue #63) --------------------------------------------------
//
// PgStore counterparts of the MemStore RenameApp tests in memstore_test.go.
// These lock down the SQL UPDATE + RETURNING shape (pgstore.go:333) and the
// mapErr → unique-violation → ErrConflict translation (pgstore.go:1470).
// pgtest.Open auto-skips when Postgres isn't reachable; on a dev box they
// pin the error contract against a real cluster (ADR-017).

// seedTwoAppsPg creates two accounts + two apps with distinct slugs and
// returns the (accountID, appID, otherAccountID, otherAppID) tuples.
func seedTwoAppsPg(t *testing.T, s *state.PgStore, ctx context.Context, a, b, slugA, slugB string) (idA, appA, idB, appB string) {
	t.Helper()
	accA, err := s.CreateAccount(ctx, a, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount A: %v", err)
	}
	accB, err := s.CreateAccount(ctx, b, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	appAResp, err := s.CreateApp(ctx, state.App{
		AccountID: accA.ID, Slug: slugA, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp A: %v", err)
	}
	appBResp, err := s.CreateApp(ctx, state.App{
		AccountID: accB.ID, Slug: slugB, Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	return accA.ID, appAResp.ID, accB.ID, appBResp.ID
}

func TestPg_RenameApp_HappyPath(t *testing.T) {
	s, ctx := pgStore(t)
	accID, _, _, _ := seedTwoAppsPg(t, s, ctx, "rename@x.com", "other@x.com", "pg-old", "pg-other")

	got, err := s.RenameApp(ctx, accID, "pg-old", "pg-new")
	if err != nil {
		t.Fatalf("RenameApp: %v", err)
	}
	if got.Slug != "pg-new" {
		t.Errorf("Slug = %q, want pg-new", got.Slug)
	}

	// Lookup by new slug must succeed; old slug must be gone.
	if _, err := s.AppBySlug(ctx, "pg-new"); err != nil {
		t.Errorf("AppBySlug(pg-new): %v", err)
	}
	if _, err := s.AppBySlug(ctx, "pg-old"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("AppBySlug(pg-old) = %v, want ErrNotFound", err)
	}
}

// TestPg_RenameApp_SlugTakenReturnsErrConflict is the load-bearing one:
// the apps.slug UNIQUE constraint (migrations/00001_init.sql:33) must
// translate via mapErr → pgerrcode.UniqueViolation → ErrConflict. If
// this test fails with a different error, the apid 409 path is broken.
func TestPg_RenameApp_SlugTakenReturnsErrConflict(t *testing.T) {
	s, ctx := pgStore(t)
	accID, _, _, _ := seedTwoAppsPg(t, s, ctx, "take@x.com", "other@x.com", "pg-victim", "pg-blocker")

	_, err := s.RenameApp(ctx, accID, "pg-victim", "pg-blocker")
	if !errors.Is(err, state.ErrConflict) {
		t.Fatalf("RenameApp onto existing slug = %v, want ErrConflict (unique violation)", err)
	}
	// Source row must be untouched.
	got, err := s.AppBySlug(ctx, "pg-victim")
	if err != nil {
		t.Fatalf("AppBySlug(pg-victim) after failed rename: %v", err)
	}
	if got.Slug != "pg-victim" {
		t.Errorf("victim.Slug = %q, want pg-victim (rename must roll back)", got.Slug)
	}
}

func TestPg_RenameApp_UnknownSlugReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	accID, _, _, _ := seedTwoAppsPg(t, s, ctx, "ghost-pg@x.com", "other@x.com", "pg-real", "pg-other")

	_, err := s.RenameApp(ctx, accID, "pg-ghost", "anything")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("RenameApp on missing slug = %v, want ErrNotFound", err)
	}
}

// TestPg_RenameApp_CrossAccountIsolation locks the WHERE clause down:
// account A's RenameApp(ctx, accA.ID, ...) MUST NOT touch account B's
// row, regardless of newSlug. The source lookup is scoped by
// account_id; without it, A could mutate B's app.
func TestPg_RenameApp_CrossAccountIsolation(t *testing.T) {
	s, ctx := pgStore(t)
	accA, _, accB, _ := seedTwoAppsPg(t, s, ctx, "pg-a@x.com", "pg-b@x.com", "pg-alpha", "pg-beta")

	// A cannot rename B's slug — must look like ErrNotFound (no row
	// matches (accA.ID, "pg-beta")), not ErrConflict.
	_, err := s.RenameApp(ctx, accA, "pg-beta", "pg-stolen")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("A renaming B's slug = %v, want ErrNotFound (account_id scope)", err)
	}

	// Untouched: B's app must still resolve to B's account.
	got, err := s.AppBySlug(ctx, "pg-beta")
	if err != nil {
		t.Fatalf("B's pg-beta vanished after cross-account rename attempt: %v", err)
	}
	if got.AccountID != accB {
		t.Errorf("pg-beta.AccountID = %q, want %q (B's account)", got.AccountID, accB)
	}

	// Spot-check via list — pg-beta must not show up under A.
	listA, err := s.ListApps(ctx, accA)
	if err != nil {
		t.Fatalf("ListApps(A): %v", err)
	}
	for _, a := range listA {
		if a.Slug == "pg-beta" {
			t.Errorf("B's pg-beta appears in A's list: %+v", a)
		}
	}
}

// TestPg_SetDeploymentFailed_PersistsCode locks the failure-specific helper
// ADR-021 introduced alongside the deployments.error_code column. The MemStore
// parity test (memstore_test.go) catches the API shape; this test catches
// the SQL. The contract being locked:
//
//   - status is pinned to 'failed' regardless of prior status.
//   - error_code carries the RFC 7807 code pkg/api.SentinelToCode lifted
//     from the wrapping error (the 'lift' is tested in pkg/imaged).
//   - error carries the free-text message for debugging.
//   - the column reads back via DeploymentByID and the read-side scanners.
//
// A regression here would silently break the M7.5 dashboard's failure-mode
// grouping and the G1 ship-blocker that PR #99 closes.
func TestPg_SetDeploymentFailed_PersistsCode(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	got, err := s.SetDeploymentFailed(ctx, depID, api.CodeImageNotFound, "oci pull failed: registry returned 404")
	if err != nil {
		t.Fatalf("SetDeploymentFailed: %v", err)
	}
	if got.Status != state.DeployFailed {
		t.Errorf("status = %s, want failed", got.Status)
	}
	if got.ErrorCode != api.CodeImageNotFound {
		t.Errorf("error_code = %q, want %q", got.ErrorCode, api.CodeImageNotFound)
	}
	if got.Error != "oci pull failed: registry returned 404" {
		t.Errorf("error = %q, want oci-pull message", got.Error)
	}

	// Round-trip via the read path used by the customer-facing API.
	read, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if read.ErrorCode != api.CodeImageNotFound {
		t.Errorf("DeploymentByID error_code = %q, want %q (scanner regression?)", read.ErrorCode, api.CodeImageNotFound)
	}
}

// TestPg_SetDeploymentFailed_EmptyCodePassesThrough covers the fallthrough
// path: a non-sentinel failure (e.g. transient network error) must still
// land in the deployments.error column but leave error_code empty. The
// dashboard branches on ErrorCode != "" to differentiate mapped codes from
// unmapped failures.
func TestPg_SetDeploymentFailed_EmptyCodePassesThrough(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	got, err := s.SetDeploymentFailed(ctx, depID, "", "net down")
	if err != nil {
		t.Fatalf("SetDeploymentFailed: %v", err)
	}
	if got.ErrorCode != "" {
		t.Errorf("error_code = %q, want empty (non-sentinel failure)", got.ErrorCode)
	}
	if got.Error != "net down" {
		t.Errorf("error = %q, want net down", got.Error)
	}
}

// TestPg_SetDeploymentFailed_UnknownReturnsErrNotFound guards the
// not-found branch — callers must not silently no-op when a stale
// deployment id is passed.
func TestPg_SetDeploymentFailed_UnknownReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, err := s.SetDeploymentFailed(ctx, "00000000-0000-0000-0000-000000000000", api.CodeImageNotFound, "x")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}
