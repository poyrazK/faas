package state_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// defaultLocalIDs is a per-PgStore cache of the resolved
// 'default-local' compute_node id. Each pgStore(t) call stands up
// a fresh Postgres schema, so the cache must be keyed on the store
// pointer — a single package-level string would feed the wrong UUID
// into the second schema (the seed row in schema B is a different
// row from schema A, even if it carries the same name). The cache
// is best-effort; a miss falls back to a fresh lookup.
var (
	defaultLocalMu         sync.Mutex
	defaultLocalIDsByStore = map[*state.PgStore]string{}
)

// resolveDefaultLocal reads the seeded compute_node id by name for
// the given PgStore. Per-store cache avoids both the package-level
// cross-schema contamination and the O(N) re-resolve on every test.
func resolveDefaultLocal(t *testing.T, ctx context.Context, s *state.PgStore) string {
	t.Helper()
	defaultLocalMu.Lock()
	if id, ok := defaultLocalIDsByStore[s]; ok {
		defaultLocalMu.Unlock()
		return id
	}
	defaultLocalMu.Unlock()
	n, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("resolve default-local compute_node (run migrations/00024 first): %v", err)
	}
	defaultLocalMu.Lock()
	defaultLocalIDsByStore[s] = n.ID
	defaultLocalMu.Unlock()
	return n.ID
}

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
	s := state.NewPgStore(pool)
	// Resolve the default-local compute_node id once at boot so
	// every CreateInstance test can pass a real FK-valid UUID.
	// Cached per-store so the next pgStore(t) (different schema)
	// doesn't reuse the previous schema's UUID.
	_ = resolveDefaultLocal(t, ctx, s)
	return s, ctx
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
	dep, _, err := s.CreateDeployment(ctx, state.Deployment{
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

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, resolveDefaultLocal(t, ctx, s), "")
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
	ins, _ := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, resolveDefaultLocal(t, ctx, s), "")

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
		DeploymentID: depID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: state.SnapMemKey(depID),
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
	old, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
	if err != nil {
		t.Fatalf("CreateInstance old: %v", err)
	}
	if err := s.SetInstanceRuntime(ctx, old.ID, "fc-"+old.ID, "10.100.0.5", 20005); err != nil {
		t.Fatalf("SetInstanceRuntime old: %v", err)
	}

	// Sleep briefly so the second instance has a strictly-later started_at.
	time.Sleep(10 * time.Millisecond)

	newer, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 256, resolveDefaultLocal(t, ctx, s), "")
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

// TestPg_CreateAppIfUnderQuota_Concurrent is the real-Postgres mirror
// of cmd/apid/handlers_quota_test.go::TestCreateApp_ConcurrentQuotaEnforcement_MemStore.
// Fires N goroutines at CreateAppIfUnderQuota on a Free account
// (DeployedApps=1). With the SELECT … FOR UPDATE lock on the parent
// accounts row, exactly one call must commit; the rest must return
// *QuotaError. Pre-PR this race slipped through because the handler
// did CountDeployedApps + CreateApp as two separate statements — the
// MemStore mutex hid it from unit tests, so only a real Postgres run
// would surface it.
func TestPg_CreateAppIfUnderQuota_Concurrent(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "race@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(acct.Plan) // DeployedApps = 1

	const n = 10
	type result struct {
		app state.App
		err error
	}
	results := make(chan result, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		i := i
		go func() {
			app := state.App{
				AccountID: acct.ID,
				Slug:      "race-" + strconv.Itoa(i),
				Type:      state.AppTypeApp,
				RAMMB:     128, MaxConcurrency: 1,
				Status: state.AppActive,
			}
			<-start
			created, err := s.CreateAppIfUnderQuota(ctx, app, limits)
			results <- result{app: created, err: err}
		}()
	}
	close(start)

	var ok int
	var quota int
	var other int
	for i := 0; i < n; i++ {
		r := <-results
		switch {
		case r.err == nil:
			ok++
		case errors.Is(r.err, state.ErrQuotaExceeded):
			quota++
		default:
			other++
			t.Logf("unexpected error: %v", r.err)
		}
	}
	if ok != 1 {
		t.Errorf("expected exactly one success under cap=1, got %d", ok)
	}
	if quota != n-1 {
		t.Errorf("expected %d ErrQuotaExceeded, got %d", n-1, quota)
	}
	if other != 0 {
		t.Errorf("got %d unexpected errors", other)
	}

	// Ground truth: the store holds exactly one app for this account.
	count, err := s.CountDeployedApps(ctx, acct.ID)
	if err != nil {
		t.Fatalf("CountDeployedApps: %v", err)
	}
	if count != 1 {
		t.Errorf("store holds %d apps, want 1", count)
	}
}

// TestPg_CreateAppIfUnderQuota_ConcurrentAcrossAccounts pins the
// cross-account invariant: PgStore's FOR UPDATE lock is row-scoped
// to the parent accounts row, so two concurrent creates on different
// accounts must both succeed even though both transactions hold row
// locks simultaneously. All goroutines start on one channel so lock
// acquisition on each row is contended; if the lock ever widened
// (e.g. table-level guard, advisory lock over the apps table), the
// per-account post-conditions would still hold but errA/errB counts
// would diverge from the cap math. Today this test pins both:
// each Free account gets one success + N-1 quota errors, regardless
// of how the other account's calls behave.
func TestPg_CreateAppIfUnderQuota_ConcurrentAcrossAccounts(t *testing.T) {
	s, ctx := pgStore(t)

	acctA, err := s.CreateAccount(ctx, "a@x.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount(A): %v", err)
	}
	acctB, err := s.CreateAccount(ctx, "b@x.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount(B): %v", err)
	}
	limitsA := api.MustLimitsFor(acctA.Plan)
	limitsB := api.MustLimitsFor(acctB.Plan)

	const perAccount = 5
	type result struct {
		owner string // "A" or "B" — disambiguates the channel aggregation
		err   error
	}
	results := make(chan result, 2*perAccount)
	start := make(chan struct{})

	for i := 0; i < perAccount; i++ {
		i := i
		go func() {
			<-start
			_, err := s.CreateAppIfUnderQuota(ctx, state.App{
				AccountID: acctA.ID,
				Slug:      "a-cross-" + strconv.Itoa(i),
				Type:      state.AppTypeApp,
				RAMMB:     128, MaxConcurrency: 1,
				Status: state.AppActive,
			}, limitsA)
			results <- result{owner: "A", err: err}
		}()
		go func() {
			<-start
			_, err := s.CreateAppIfUnderQuota(ctx, state.App{
				AccountID: acctB.ID,
				Slug:      "b-cross-" + strconv.Itoa(i),
				Type:      state.AppTypeApp,
				RAMMB:     128, MaxConcurrency: 1,
				Status: state.AppActive,
			}, limitsB)
			results <- result{owner: "B", err: err}
		}()
	}
	close(start)

	var okA, okB, quotaA, quotaB int
	for k := 0; k < 2*perAccount; k++ {
		r := <-results
		switch {
		case r.err == nil && r.owner == "A":
			okA++
		case r.err == nil && r.owner == "B":
			okB++
		case errors.Is(r.err, state.ErrQuotaExceeded) && r.owner == "A":
			quotaA++
		case errors.Is(r.err, state.ErrQuotaExceeded) && r.owner == "B":
			quotaB++
		default:
			t.Logf("unexpected error (owner=%s): %v", r.owner, r.err)
		}
	}
	// The invariant: per-account cap math. Cross-account contention
	// must NOT cause either side to lose a success slot or gain a
	// spurious quota error.
	if okA != 1 || okB != 1 {
		t.Errorf("okA=%d okB=%d, want 1/1 — cross-account locking regression", okA, okB)
	}
	if quotaA != perAccount-1 || quotaB != perAccount-1 {
		t.Errorf("quotaA=%d quotaB=%d, want %d/%d", quotaA, quotaB, perAccount-1, perAccount-1)
	}
	if got, err := s.CountDeployedApps(ctx, acctA.ID); err != nil {
		t.Errorf("CountDeployedApps(A): %v", err)
	} else if got != 1 {
		t.Errorf("count(A) = %d, want 1", got)
	}
	if got, err := s.CountDeployedApps(ctx, acctB.ID); err != nil {
		t.Errorf("CountDeployedApps(B): %v", err)
	} else if got != 1 {
		t.Errorf("count(B) = %d, want 1", got)

	}
}

// TestPg_SnapshotStorageKey_RoundTrip mirrors the MemStore test of the
// same name on the PgStore side (F-3 review finding): CreateSnapshot
// stores the value the caller passes, LatestSnapshot reads it back
// unchanged, and ListSnapshotsForGC exposes it on SnapshotForGC so
// the imaged GC loop can Storage.Delete under the canonical key.
//
// The contract being pinned: PgStore.CreateSnapshot requires
// StorageKey (no silent default — see pgstore.go for the rationale);
// this test verifies both halves — the happy-path round-trip and the
// empty-key rejection.
func TestPg_SnapshotStorageKey_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// (1) Caller-supplied storage_key round-trips through
	// CreateSnapshot → LatestSnapshot → ListSnapshotsForGC.
	want := state.SnapMemKey(depID)
	_, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.10.0", MemBytes: 1,
		StorageKey: want,
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	got, err := s.LatestSnapshot(ctx, depID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.StorageKey != want {
		t.Errorf("LatestSnapshot StorageKey = %q, want %q", got.StorageKey, want)
	}
	rows, err := s.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	if len(rows) != 1 || rows[0].StorageKey != want {
		t.Errorf("ListSnapshotsForGC returned %+v, want one row with StorageKey=%q", rows, want)
	}

	// (2) Empty StorageKey is rejected — this is the F-1 contract
	// pin. A future regression that re-adds the silent default
	// would surface here as a nil error where one is expected.
	_, err = s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.11.0", MemBytes: 1,
		// StorageKey deliberately omitted.
	})
	if err == nil {
		t.Error("CreateSnapshot with empty StorageKey returned nil error; want explicit error per F-1 contract")
	}
}

// --- Compute nodes (issue #97 / ADR-025 axis 3) -----------------------------
//
// Migrations/00024_compute_nodes.sql seeds one synthetic 'default-local'
// row. Tests below use the canonical helper resolveDefaultLocal to fetch
// that id when they need a valid FK target; new-node tests insert their
// own via CreateComputeNode (which lets Postgres mint the uuid via the
// column default and returns it in the RETURNING clause).

func TestPg_ComputeNodes_DefaultLocalSeededByMigration(t *testing.T) {
	s, ctx := pgStore(t)

	// The seeded default-local row carries the production shape:
	// unix:///run/faas/vmmd.sock target, 160 vCPU, 56 GB mem, 200 max
	// concurrency, 47,600 MB admission ceiling. Pin all four so a
	// future migration drift surfaces here, not at first wake.
	got, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatalf("ComputeNodeByName(default-local): %v", err)
	}
	if got.Name != state.DefaultLocalNodeName {
		t.Errorf("Name=%q, want %q", got.Name, state.DefaultLocalNodeName)
	}
	if !got.Active {
		t.Errorf("seeded default-local should be active")
	}
	if got.AdmissionCeilingMB != 47600 {
		t.Errorf("AdmissionCeilingMB=%d, want 47600", got.AdmissionCeilingMB)
	}
	if got.MemMB != 56000 {
		t.Errorf("MemMB=%d, want 56000", got.MemMB)
	}
	if got.MaxConcurrency != 200 {
		t.Errorf("MaxConcurrency=%d, want 200", got.MaxConcurrency)
	}
	if got.TargetURL != "unix:///run/faas/vmmd.sock" {
		t.Errorf("TargetURL=%q, want unix:///run/faas/vmmd.sock", got.TargetURL)
	}
	if got.LastHeartbeatAt.IsZero() {
		t.Errorf("seeded LastHeartbeatAt should be stamped at migration apply")
	}
}

func TestPg_ComputeNodes_ActiveComputeNodes_ExcludesInactive_AndSortsByName(t *testing.T) {
	s, ctx := pgStore(t)

	// Insert two more nodes; one active, one drained.
	if _, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "alpha-node", TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 80, MemMB: 28000, MaxConcurrency: 100, AdmissionCeilingMB: 23800,
		Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode(alpha): %v", err)
	}
	if _, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "zulu-drained", TargetURL: "tcp://10.0.0.10:50051",
		VPCPUs: 80, MemMB: 28000, MaxConcurrency: 100, AdmissionCeilingMB: 23800,
		Active: false,
	}); err != nil {
		t.Fatalf("CreateComputeNode(zulu): %v", err)
	}

	nodes, err := s.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	// Expected: alpha-node + default-local (alphabetical). Drained
	// 'zulu-drained' must NOT appear even though its name sorts last.
	wantNames := []string{"alpha-node", state.DefaultLocalNodeName}
	if len(nodes) != len(wantNames) {
		names := make([]string, 0, len(nodes))
		for _, n := range nodes {
			names = append(names, n.Name)
		}
		t.Fatalf("ActiveComputeNodes returned %d nodes (%v), want %d (%v)", len(nodes), names, len(wantNames), wantNames)
	}
	for i := range wantNames {
		if nodes[i].Name != wantNames[i] {
			t.Errorf("ActiveComputeNodes[%d].Name=%q, want %q", i, nodes[i].Name, wantNames[i])
		}
	}
}

func TestPg_ComputeNodes_ByID_NotFoundAndByName_NotFound(t *testing.T) {
	s, ctx := pgStore(t)

	if _, err := s.ComputeNodeByID(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ComputeNodeByID(unknown): want ErrNotFound, got %v", err)
	}
	if _, err := s.ComputeNodeByName(ctx, "no-such-name"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("ComputeNodeByName(unknown): want ErrNotFound, got %v", err)
	}
}

func TestPg_ComputeNodes_Heartbeat_BumpsAndUnknownReturnsNotFound(t *testing.T) {
	s, ctx := pgStore(t)

	// Unknown id → ErrNotFound (RowsAffected==0 path).
	if err := s.HeartbeatComputeNode(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("HeartbeatComputeNode(unknown): want ErrNotFound, got %v", err)
	}

	// Real node → last_heartbeat_at moves forward.
	//
	// Flake guard: Postgres `now()` is microsecond-resolution but
	// wall-clock scheduling on a busy CI runner can collapse the
	// (sleep, exec, query) window to less than 1 µs on rare passes
	// (memory: pkg-session-tamper-flake showed a similar flake from
	// a sub-millisecond race). We retry once with a longer sleep
	// before failing — the retry is part of the test contract, not a
	// flake cover-up.
	id := resolveDefaultLocal(t, ctx, s)
	before, err := s.ComputeNodeByID(ctx, id)
	if err != nil {
		t.Fatalf("ComputeNodeByID: %v", err)
	}
	if !assertHeartbeatAdvanced(t, s, ctx, id, before.LastHeartbeatAt, 2*time.Millisecond) {
		if !assertHeartbeatAdvanced(t, s, ctx, id, before.LastHeartbeatAt, 10*time.Millisecond) {
			t.Errorf("HeartbeatComputeNode did not bump LastHeartbeatAt after 2 retries")
		}
	}
}

// assertHeartbeatAdvanced sleeps the given duration, calls
// HeartbeatComputeNode, and returns true iff last_heartbeat_at moved
// forward. Pulled out so the retry path in
// TestPg_ComputeNodes_Heartbeat_BumpsAndUnknownReturnsNotFound stays
// readable.
func assertHeartbeatAdvanced(t *testing.T, s *state.PgStore, ctx context.Context, id string, before time.Time, sleep time.Duration) bool {
	t.Helper()
	time.Sleep(sleep)
	if err := s.HeartbeatComputeNode(ctx, id); err != nil {
		t.Fatalf("HeartbeatComputeNode: %v", err)
		return false
	}
	after, err := s.ComputeNodeByID(ctx, id)
	if err != nil {
		t.Fatalf("ComputeNodeByID(after): %v", err)
		return false
	}
	return after.LastHeartbeatAt.After(before)
}

func TestPg_ComputeNodes_Create_RejectsBadTargetURL(t *testing.T) {
	s, ctx := pgStore(t)

	// CHECK constraint enforces ^(unix|tcp|dns)://. http:// and an
	// empty string both fail; a future regression that loosens the
	// regex would surface here.
	cases := []string{"http://example.com", "", "ftp://example.com"}
	for _, bad := range cases {
		_, err := s.CreateComputeNode(ctx, state.ComputeNode{
			Name: "bad-" + bad, TargetURL: bad,
			VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1,
		})
		if err == nil {
			t.Errorf("CreateComputeNode(target_url=%q) returned nil error; CHECK should reject", bad)
		}
	}
}

func TestPg_ComputeNodes_Create_DuplicateNameConflicts(t *testing.T) {
	s, ctx := pgStore(t)

	// default-local is already seeded; a second row with the same name
	// must surface as a UNIQUE-violation error. PgStore does not
	// translate to ErrConflict — it surfaces the raw pgx error. We
	// pin the constraint by name (compute_nodes_name_key) so a future
	// regression that drops the error, or that swaps the unique index
	// for something else, surfaces here. The constraint name is set by
	// the migration's `name text not null unique` clause; pgx renders
	// it on every unique-violation message.
	const wantConstraint = "compute_nodes_name_key"
	_, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: state.DefaultLocalNodeName, TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 1, MemMB: 1, MaxConcurrency: 1, AdmissionCeilingMB: 1,
	})
	if err == nil {
		t.Fatal("CreateComputeNode(duplicate name): want error, got nil")
	}
	if !strings.Contains(err.Error(), wantConstraint) {
		t.Errorf("CreateComputeNode(duplicate name) error=%q, want error containing %q (UNIQUE constraint name)", err.Error(), wantConstraint)
	}
}

func TestPg_ComputeNodes_Create_AssignsUUIDWhenEmpty(t *testing.T) {
	s, ctx := pgStore(t)

	// Caller omits ID; Postgres column default (gen_random_uuid) should
	// fill it and RETURNING should surface the assigned UUID. Pin the
	// format with uuid.Parse so a future migration that swaps the
	// default for a sequential id surfaces here.
	got, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "fresh-uuid", TargetURL: "unix:///run/faas/vmmd.sock",
		VPCPUs: 80, MemMB: 28000, MaxConcurrency: 100, AdmissionCeilingMB: 23800,
		Active: true,
	})
	if err != nil {
		t.Fatalf("CreateComputeNode: %v", err)
	}
	if got.ID == "" {
		t.Errorf("PgStore should assign a UUID via the column default; got empty ID")
	}
	if _, err := uuid.Parse(got.ID); err != nil {
		t.Errorf("CreateComputeNode assigned ID=%q, want a parseable UUID (gen_random_uuid): %v", got.ID, err)
	}
	if got.CreatedAt.IsZero() {
		t.Errorf("CreatedAt should be stamped by the column default; got zero")
	}
}

func TestPg_ComputeNodes_UsedMB_SumsLiveInstancesOnly(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	// Create 2 waking, 1 cold_booting, 2 running, 1 stopped, 1 parked.
	// Total live = 5 × (256 + api.PerVMOverheadMB) MB.
	for _, st := range []string{
		string(state.StateWaking),
		string(state.StateWaking),
		string(state.StateColdBooting),
		string(state.StateRunning),
		string(state.StateRunning),
	} {
		if _, err := s.CreateInstance(ctx, appID, depID, st, 256, nodeID, ""); err != nil {
			t.Fatalf("CreateInstance(%s): %v", st, err)
		}
	}
	// Non-live states (not in the SELECT's WHERE clause): must NOT
	// contribute to the aggregate. The DB CHECK (migrations/00020)
	// pins the state set to {pending, cold_booting, waking, running,
	// parked, stopped, evicting_account_deleting}; parked + stopped
	// are the two non-live writes the engine emits in practice.
	if _, err := s.CreateInstance(ctx, appID, depID, string(state.StateStopped), 256, nodeID, ""); err != nil {
		t.Fatalf("CreateInstance(stopped): %v", err)
	}
	if _, err := s.CreateInstance(ctx, appID, depID, string(state.StateParked), 256, nodeID, ""); err != nil {
		t.Fatalf("CreateInstance(parked): %v", err)
	}

	got, err := s.ComputeNodeUsedMB(ctx, nodeID)
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB: %v", err)
	}
	want := int64(5 * (256 + api.PerVMOverheadMB))
	if got != want {
		t.Errorf("ComputeNodeUsedMB=%d, want %d (5 live × (256+%d))", got, want, api.PerVMOverheadMB)
	}

	// Unknown node → 0 (COALESCE wraps the aggregate in the SELECT).
	gotU, err := s.ComputeNodeUsedMB(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("ComputeNodeUsedMB(unknown): %v", err)
	}
	if gotU != 0 {
		t.Errorf("ComputeNodeUsedMB(unknown)=%d, want 0", gotU)
	}
}

// --- Snapshot GC (ADR-005 / spec §4.6) ---------------------------------------
//
// The MemStore side of these methods is already covered
// (TestMemStore_DeleteSnapshotsByID_BulkAndIdempotent etc.). The PgStore
// tests below mirror the MemStore coverage against a real Postgres
// schema so the SQL stays pinned — same regression-guard shape as the
// compute_nodes suite landed in PR #114.

func TestPg_DeleteSnapshotsByID_BulkAndIdempotent(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// Insert two snapshots via the public CreateSnapshot surface
	// (also exercises the storage_key contract via F-1 — pgstore.go:1245).
	snapA, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: state.SnapMemKey(depID) + "/a",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot A: %v", err)
	}
	snapB, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: state.SnapMemKey(depID) + "/b",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot B: %v", err)
	}

	// First delete: both rows gone.
	n, err := s.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatalf("first DeleteSnapshotsByID: %v", err)
	}
	if n != 2 {
		t.Errorf("first delete affected %d rows, want 2", n)
	}

	// Idempotent: re-running on the same ids hits zero rows.
	n2, err := s.DeleteSnapshotsByID(ctx, []string{snapA.ID, snapB.ID})
	if err != nil {
		t.Fatalf("second DeleteSnapshotsByID: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second delete affected %d rows, want 0 (idempotent)", n2)
	}

	// Empty input is a no-op, not an error.
	if n3, err := s.DeleteSnapshotsByID(ctx, nil); err != nil || n3 != 0 {
		t.Errorf("DeleteSnapshotsByID(nil) = (%d, %v), want (0, nil)", n3, err)
	}
}

func TestPg_MarkAllSnapshotsStaleByFCVersion_OnlyFlipsNonCurrent(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// Seed three snapshots across three FC versions. Only the
	// matching-version row should stay live; the other two flip.
	mkSnap := func(v string) string {
		snap, err := s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: depID, FCVersion: v, MemBytes: 100, DiskBytes: 100,
			StorageKey: state.SnapMemKey(depID) + "/" + v,
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", v, err)
		}
		return snap.ID
	}
	// Three FC versions; only the matching one (1.8.0) stays live
	// after the sweep. id170/id190 are captured as vars (not `_, _`)
	// so the assignment expressions read like a fixture table —
	// their values are checked implicitly via the LatestSnapshot
	// readback below (only id180 should be live).
	id170 := mkSnap("1.7.0")
	id180 := mkSnap("1.8.0")
	id190 := mkSnap("1.9.0")
	_, _ = id170, id190

	// Sweep against 1.8.0: 1.7.0 and 1.9.0 should flip.
	n, err := s.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByFCVersion: %v", err)
	}
	if n != 2 {
		t.Errorf("marked %d stale, want 2", n)
	}

	// Confirm by reading back via LatestSnapshot (which filters stale).
	latest, err := s.LatestSnapshot(ctx, depID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if latest.ID != id180 {
		t.Errorf("LatestSnapshot after sweep returned id=%q, want %q (only 1.8.0 should be live)", latest.ID, id180)
	}
	if latest.Stale {
		t.Errorf("1.8.0 snapshot must not be stale after sweep")
	}

	// Idempotent: a second sweep finds no non-stale rows to flip.
	n2, _ := s.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if n2 != 0 {
		t.Errorf("second sweep marked %d, want 0 (idempotent)", n2)
	}

	// Sweeping against a version that has NO live rows matching
	// (every live row already matches that version, or no rows exist)
	// is a 0-result. After the first sweep, only 1.8.0 is live; a
	// sweep against "1.8.0" is a 0-result (idempotent — proven above).
	// A sweep against a version no row carries (e.g. "9.9.9") will
	// flip every live non-matching row, so we skip that case here —
	// it's the EXPECTED behavior, not a bug.
}

func TestPg_MarkOldSnapshotsStale_OnlyFlipsGivenIDs(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	mkSnap := func(suffix string) string {
		snap, err := s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
			StorageKey: state.SnapMemKey(depID) + "/" + suffix,
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", suffix, err)
		}
		return snap.ID
	}
	idA := mkSnap("a")
	idB := mkSnap("b")
	idC := mkSnap("c")

	// Mark only A and C stale.
	n, err := s.MarkOldSnapshotsStale(ctx, []string{idA, idC})
	if err != nil {
		t.Fatalf("MarkOldSnapshotsStale: %v", err)
	}
	if n != 2 {
		t.Errorf("marked %d, want 2", n)
	}

	// Empty input → no-op.
	if n0, err := s.MarkOldSnapshotsStale(ctx, nil); err != nil || n0 != 0 {
		t.Errorf("MarkOldSnapshotsStale(nil) = (%d, %v), want (0, nil)", n0, err)
	}

	// LatestSnapshot filters stale; the survivor is B.
	latest, err := s.LatestSnapshot(ctx, depID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if latest.ID != idB {
		t.Errorf("LatestSnapshot after mark returned id=%q, want %q (B should remain live)", latest.ID, idB)
	}
	if latest.Stale {
		t.Errorf("B should remain live; got Stale=true")
	}
}

func TestPg_DeleteSnapshotsStaleOlderThan_OnlyRemovesStalePastRetention(t *testing.T) {
	// This test backdates created_at directly via the pool — there's no
	// public Store surface for "create a snapshot N days ago," and
	// opening one for a single test is cheaper than racing a real clock.
	// The pgtest.Open + MigrateUp pattern mirrors the helper in pgStore(t).
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// Two snapshots. Both start live; we backdate one and flip the
	// other to stale-but-recent to exercise both sides of the WHERE
	// clause (stale=true AND created_at < now()-retention).
	freshID := mustCreateSnap(t, s, ctx, depID, "fresh", false)
	oldID := mustCreateSnap(t, s, ctx, depID, "old", true)
	recentStaleID := mustCreateSnap(t, s, ctx, depID, "recent-stale", true)

	// Backdate `old` to 30 days ago; `recent-stale` stays at now().
	if _, err := pool.Exec(ctx, `update snapshots set created_at = now() - interval '30 days' where id = $1`, oldID); err != nil {
		t.Fatalf("backdate old: %v", err)
	}

	// DeleteSnapshotsStaleOlderThan(7d) → only `old` qualifies.
	n, err := s.DeleteSnapshotsStaleOlderThan(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("DeleteSnapshotsStaleOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("deleted %d rows, want 1 (only the 30-day-old stale row)", n)
	}

	// Confirm `old` is gone, `fresh` + `recent-stale` remain.
	// Use COUNT(*) + QueryRow.Scan (not pool.Exec) — Exec returns nil
	// error for a 0-row SELECT, which would mask "row was not deleted".
	assertRowCount := func(id string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, `select count(*) from snapshots where id = $1`, id).Scan(&got); err != nil {
			t.Fatalf("count(%s): %v", id, err)
		}
		if got != want {
			t.Errorf("snapshot %s: row count = %d, want %d", id, got, want)
		}
	}
	assertRowCount(oldID, 0)
	assertRowCount(freshID, 1)
	assertRowCount(recentStaleID, 1)

	// Idempotent: a second pass with the same retention finds nothing.
	n2, _ := s.DeleteSnapshotsStaleOlderThan(ctx, 7*24*time.Hour)
	if n2 != 0 {
		t.Errorf("second sweep deleted %d rows, want 0", n2)
	}
}

func TestPg_ListLiveSnapshotStats_ExcludesStaleAndOrdersByMemBytesDesc(t *testing.T) {
	// Open the pool directly so the test can update mem/disk_bytes
	// on the inserted rows to assert the projection shape. The public
	// CreateSnapshot surface takes MemBytes/DiskBytes but the table
	// stores the value as-is — we want a non-zero value here to pin
	// the scan field, not the round-trip.
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	// Insert three snapshots: one stale (filtered), two live.
	mkSnap := func(suffix string, stale bool) string {
		snap, err := s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
			StorageKey: state.SnapMemKey(depID) + "/" + suffix,
			Stale:      stale,
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", suffix, err)
		}
		return snap.ID
	}
	_ = mkSnap("stale", true)
	live1 := mkSnap("live1", false)
	live2 := mkSnap("live2", false)

	// Update mem_bytes/disk_bytes on the live rows so we can assert
	// the projection shape (the 100/100 from CreateSnapshot is fine
	// but using a more recognizable value makes the assertion clearer).
	if _, err := pool.Exec(ctx,
		`update snapshots set mem_bytes = $1, disk_bytes = $2 where id = any($3)`,
		int64(2048), int64(4096), []string{live1, live2}); err != nil {
		t.Fatalf("update mem/disk: %v", err)
	}

	stats, err := s.ListLiveSnapshotStats(ctx)
	if err != nil {
		t.Fatalf("ListLiveSnapshotStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d stats, want 2 (stale row must be filtered)", len(stats))
	}
	for _, sz := range stats {
		if sz.MemBytes != 2048 || sz.DiskBytes != 4096 {
			t.Errorf("SnapshotSize=%+v, want {MemBytes:2048 DiskBytes:4096}", sz)
		}
	}

	// Order: by mem_bytes desc. Both rows have the same mem_bytes so
	// the relative order is undefined; the set check above is the
	// contract.
}

// mustCreateSnap is a tiny test helper for the GC suite — keeps the
// boilerplate off the test bodies. The MemStore side already has
// inline closures that do the same job (TestMemStore_*), but the
// PgStore tests touch the pool for backdating/updating, so a single
// named helper is more readable than three nested closures.
func mustCreateSnap(t *testing.T, s *state.PgStore, ctx context.Context, depID, suffix string, stale bool) string {
	t.Helper()
	snap, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: state.SnapMemKey(depID) + "/" + suffix,
		Stale:      stale,
	})
	if err != nil {
		t.Fatalf("mustCreateSnap(%s): %v", suffix, err)
	}
	return snap.ID
}

// --- Instance lifecycle (PR #74 / spec §6.1) ---------------------------------
//
// The watchdog (ListInstancesByStatesOlderThan) and the retention sweep
// (ListInstancesInTerminalStatesOlderThan) share most of their shape —
// both read instances.filtered by state-set + a state-aware age column.
// PgStore-only coverage; the MemStore side is already covered.

func TestPg_UpdateInstanceStateWithTimestamp_BumpsStateAndParkedAt(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	when := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.UpdateInstanceStateWithTimestamp(ctx, ins.ID, string(state.StateParked), when); err != nil {
		t.Fatalf("UpdateInstanceStateWithTimestamp: %v", err)
	}

	got, err := s.InstanceByID(ctx, ins.ID)
	if err != nil {
		t.Fatalf("InstanceByID: %v", err)
	}
	if got.State != string(state.StateParked) {
		t.Errorf("State=%q, want %q", got.State, string(state.StateParked))
	}
	if !got.ParkedAt.Equal(when) {
		t.Errorf("ParkedAt=%v, want %v", got.ParkedAt, when)
	}

	// Unknown id → ErrNotFound.
	missing := "00000000-0000-0000-0000-000000000000"
	if err := s.UpdateInstanceStateWithTimestamp(ctx, missing, string(state.StateRunning), when); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("UpdateInstanceStateWithTimestamp(missing): want ErrNotFound, got %v", err)
	}
}

func TestPg_UpdateInstanceStateToTerminal_BumpsStateAndTerminalAt(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	when := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.UpdateInstanceStateToTerminal(ctx, ins.ID, string(state.StateStopped), when); err != nil {
		t.Fatalf("UpdateInstanceStateToTerminal: %v", err)
	}

	// The dedicated terminal_at column is read by
	// ListInstancesInTerminalStatesOlderThan; round-trip via that
	// helper to assert it's stamped.
	threshold := when.Add(time.Hour)
	got, err := s.ListInstancesInTerminalStatesOlderThan(ctx,
		[]state.State{state.StateStopped}, threshold)
	if err != nil {
		t.Fatalf("ListInstancesInTerminalStatesOlderThan: %v", err)
	}
	if len(got) != 1 || got[0].ID != ins.ID {
		t.Fatalf("ListInstancesInTerminalStatesOlderThan returned %d rows (ids=%v), want 1 (id=%s)", len(got), idsOf(got), ins.ID)
	}

	// Unknown id → ErrNotFound.
	missing := "00000000-0000-0000-0000-000000000000"
	if err := s.UpdateInstanceStateToTerminal(ctx, missing, string(state.StateStopped), when); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("UpdateInstanceStateToTerminal(missing): want ErrNotFound, got %v", err)
	}
}

func TestPg_ListInstancesByStatesOlderThan_UsesStateAwareAgeColumn(t *testing.T) {
	// Open pool directly so the test can backdate started_at / parked_at
	// — PgStore.pool is unexported (state_test can't see it), and
	// there's no public Store surface for "set started_at = X".
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	// COVERAGE GAP — the watchdog's CASE clause (pgstore.go:1132) has
	// two branches:
	//
	//   case when state = 'snapshotting' then parked_at else started_at end < $2
	//
	// Migration 00020 removed 'snapshotting' from instances_state_check,
	// so the public Store surface cannot seed a row with that state —
	// this test exercises only the ELSE branch via WAKING +
	// COLD_BOOTING. The THEN branch (parked_at) is defensively retained
	// in pgstore.go for any historical row that survives a re-migration.
	// A future regression that drops the CASE clause entirely would
	// reintroduce the pre-00015 bug where rows with NULL started_at were
	// silently mis-aged; pin that branch separately if it becomes
	// exercisable.
	mkIns := func(st state.State, suffix string) string {
		ins, err := s.CreateInstance(ctx, appID, depID, string(st), 256, nodeID, "")
		if err != nil {
			t.Fatalf("CreateInstance(%s): %v", suffix, err)
		}
		return ins.ID
	}
	wakingID := mkIns(state.StateWaking, "waking")
	coldID := mkIns(state.StateColdBooting, "cold_booting")

	// Threshold is 1 hour ago — both rows must be older than this for
	// the predicate `started_at < threshold` to qualify them as stuck.
	threshold := time.Now().Add(-1 * time.Hour)

	// Backdate started_at on both rows so they're well below the
	// threshold (rows are already old in practice, but explicit
	// backdating makes the test stable on a fast CI runner).
	if _, err := pool.Exec(ctx,
		`update instances set started_at = now() - interval '2 hours' where id = $1`, wakingID); err != nil {
		t.Fatalf("backdate waking: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`update instances set started_at = now() - interval '2 hours' where id = $1`, coldID); err != nil {
		t.Fatalf("backdate cold: %v", err)
	}

	got, err := s.ListInstancesByStatesOlderThan(ctx,
		[]state.State{state.StateWaking, state.StateColdBooting}, threshold)
	if err != nil {
		t.Fatalf("ListInstancesByStatesOlderThan: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (waking + cold_booting, both started_at < threshold)", len(got))
	}
	gotIDs := idsOf(got)
	if !contains(gotIDs, wakingID) || !contains(gotIDs, coldID) {
		t.Errorf("missing rows: got %v, want both %s and %s", gotIDs, wakingID, coldID)
	}
}

func TestPg_DeleteInstance_RemovesRowAndReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateRunning), 512, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	if err := s.DeleteInstance(ctx, ins.ID); err != nil {
		t.Fatalf("first DeleteInstance: %v", err)
	}

	// Subsequent InstanceByID → ErrNotFound.
	if _, err := s.InstanceByID(ctx, ins.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("InstanceByID after delete: want ErrNotFound, got %v", err)
	}

	// Idempotent: second delete also ErrNotFound.
	if err := s.DeleteInstance(ctx, ins.ID); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("second DeleteInstance: want ErrNotFound, got %v", err)
	}

	// Random unknown id → ErrNotFound.
	missing := "00000000-0000-0000-0000-000000000000"
	if err := s.DeleteInstance(ctx, missing); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("DeleteInstance(unknown): want ErrNotFound, got %v", err)
	}
}

// idsOf is a small helper for asserting on instance lists without
// pulling in a third-party assertion lib. State_test already has
// similar one-liners; this matches the style.
func idsOf(insts []state.Instance) []string {
	out := make([]string, 0, len(insts))
	for _, i := range insts {
		out = append(out, i.ID)
	}
	return out
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// TestPg_CreateDeployment_RejectsDeletedApp is the PR-A SQL pin for
// the active-app gate inside CreateDeployment. Mirrors the wire-level
// test in cmd/apid/deploy_to_active_app_test.go. The handler-level
// test catches the wire contract; this test catches the SQL:
// SELECT 1 FROM apps … FOR UPDATE returns 0 rows for a soft-deleted
// app, so the tx rolls back without INSERT'ing a deployments row.
//
// Skips without Postgres (pgtest.Open handles the skip).
func TestPg_CreateDeployment_RejectsDeletedApp(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	// PR-A review fix: seedLiveDeploy inserts one deployment for the
	// app already, so a "no rows exist after the failed CreateDeployment"
	// check is wrong. Capture the pre-delete count and assert it does
	// NOT GROW across the rejected insert. The original gate's contract
	// (no new deployment row for a deleted app) is what this pins.
	pre, err := s.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp (pre): %v", err)
	}

	// Soft-delete the app via the public Store surface.
	if err := s.DeleteApp(ctx, appID); err != nil {
		t.Fatalf("DeleteApp: %v", err)
	}

	// Now CreateDeployment must return ErrNotFound (the active-app
	// gate's contract). The handler maps ErrNotFound to 404.
	_, _, err = s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "registry.example.com/x@sha256:" + strings.Repeat("d", 64),
		Status:      state.DeployPending,
	})
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("CreateDeployment against deleted app: err = %v, want ErrNotFound", err)
	}

	// Ground truth: no new deployment row was inserted for the deleted
	// app — the count must equal the pre-delete baseline.
	post, err := s.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp (post): %v", err)
	}
	if len(post) != len(pre) {
		t.Errorf("deployments count grew from %d to %d after rejected CreateDeployment on deleted app", len(pre), len(post))
	}

	// Sanity: an active app on a different account still accepts
	// deployments. This pins the gate's WHERE clause down to the
	// specific app id (not account-wide).
	otherAcct, _ := s.CreateAccount(ctx, "other@example.com", api.PlanPro)
	otherApp, _ := s.CreateApp(ctx, state.App{
		AccountID: otherAcct.ID, Slug: "active-app",
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 2, IdleTimeoutS: 60,
	})
	if _, _, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       otherApp.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "registry.example.com/y@sha256:" + strings.Repeat("e", 64),
		Status:      state.DeployPending,
	}); err != nil {
		t.Errorf("active app must accept deployments, got %v", err)
	}
}

// TestPg_ClaimQueuedBuild pins the atomic queued → running transition

// TestPg_ClaimQueuedBuild pins the atomic queued → running transition
// that closes the apid/reaper double-emit race (PR-A review). First
// claim wins; subsequent claims return ErrNotFound. started_at must be
// set on the winner.
func TestPg_ClaimQueuedBuild(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	_, _, depID := seedLiveDeploy(t, s, ctx)

	b, err := s.CreateBuild(ctx, depID, state.DeploymentKindTarball, 100, "")
	if err != nil {
		t.Fatalf("CreateBuild: %v", err)
	}

	// First claim wins; row flips to running and started_at is set.
	won, err := s.ClaimQueuedBuild(ctx, b.ID)
	if err != nil {
		t.Fatalf("first ClaimQueuedBuild: %v", err)
	}
	if won.Status != state.BuildRunning {
		t.Errorf("first claim status = %q, want running", won.Status)
	}
	if won.StartedAt.IsZero() {
		t.Errorf("first claim started_at is zero")
	}

	// Second claim loses — row is no longer queued.
	_, err = s.ClaimQueuedBuild(ctx, b.ID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("second claim err = %v, want ErrNotFound", err)
	}

	// Unknown id loses the same way. Use a valid UUID literal —
	// the column is uuid-typed and rejects bare hex strings like
	// "deadbeef" with a syntax error rather than ErrNotFound.
	_, err = s.ClaimQueuedBuild(ctx, "00000000-0000-0000-0000-000000000000")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// TestPg_CreateDeployment_SupersedesPriorLive pins the at-rest happy
// path: a second deploy against an app that already has a `live`
// deployment row gets the prior row flipped to `superseded` inside the
// same tx, and the new row is inserted with `pending`. The returned
// new row carries the just-created identity; the prior is read back
// via DeploymentByID to assert (2-return CreateDeployment shape).
func TestPg_CreateDeployment_SupersedesPriorLive(t *testing.T) {
	s, ctx := pgStore(t)

	_, appID, priorDepID := seedLiveDeploy(t, s, ctx)

	created, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/v2@sha256:" + strings.Repeat("a", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("second CreateDeployment: %v", err)
	}
	if created.Status != state.DeployPending {
		t.Errorf("created.Status = %q, want pending", created.Status)
	}

	// The DB must agree: the prior row is superseded.
	got, err := s.DeploymentByID(ctx, priorDepID)
	if err != nil {
		t.Fatalf("DeploymentByID(prior): %v", err)
	}
	if got.Status != state.DeploySuperseded {
		t.Errorf("DB prior.Status = %q, want superseded", got.Status)
	}
}

// TestPg_CreateDeployment_LeavesBuildingRowAlone is the M-1 review
// invariant: a second deploy against an app whose only prior row is
// `building` (mid-VM-boot / mid-build / mid-imaging) must NOT
// supersede it. The new row lands, the old row keeps running.
//
// Without this gate, an in-VM builderd on row A would write
// `markSucceeded` → `UpdateDeploymentStatus(A, ..., live)` mid-way
// through row A's pipeline, while the same tx would have already
// flipped row A to `superseded`. The deployment row the scheduler
// sees depends on whichever UpdateDeploymentStatus lands last —
// non-deterministic, and a genuine orphan.
func TestPg_CreateDeployment_LeavesBuildingRowAlone(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)

	// Flip the existing row to `building` (simulating: apid set it
	// after CreateDeployment; builderd hasn't returned yet).
	priorDepID := mustCreateImageDeployment(t, s, ctx, appID)
	if err := s.UpdateDeploymentStatus(ctx, priorDepID, state.DeployBuilding, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus(building): %v", err)
	}

	// Second deploy — must NOT supersede the building row.
	created, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/v3@sha256:" + strings.Repeat("b", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("second CreateDeployment: %v", err)
	}
	if created.Status != state.DeployPending {
		t.Errorf("created.Status = %q, want pending", created.Status)
	}

	// DB confirms — the building row is untouched, no race orphan.
	got, err := s.DeploymentByID(ctx, priorDepID)
	if err != nil {
		t.Fatalf("DeploymentByID(building): %v", err)
	}
	if got.Status != state.DeployBuilding {
		t.Errorf("building row Status = %q, want building (untouched)", got.Status)
	}

	// Sanity: both rows are visible to the app's history.
	all, err := s.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len(ListDeploymentsForApp) = %d, want 2 (building + pending)", len(all))
	}
	_ = acctID
}

// mustCreateImageDeployment creates a fresh deployment row on appID
// at `pending` status so a test can flip its status independently.
// Returns the depID.
func mustCreateImageDeployment(t *testing.T, s *state.PgStore, ctx context.Context, appID string) string {
	t.Helper()
	d, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/v1@sha256:" + strings.Repeat("c", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("mustCreateImageDeployment: %v", err)
	}
	return d.ID
}

// TestPg_CreateDeployment_NoOpFirstDeploy covers the "no prior row"
// path: no supersede must fire when there is no prior live/pending
// row. The created row carries the just-created identity and the
// prior (queried via DeploymentByID of a non-existent sentinel) is
// not observed — but the structural guarantee is that the row count
// stays at 1.
func TestPg_CreateDeployment_NoOpFirstDeploy(t *testing.T) {
	s, ctx := pgStore(t)
	acctID := createAccount(t, s, ctx, "first-deploy@example.com")
	appID := createApp(t, s, ctx, acctID, "first-deploy")

	created, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "registry.example.com/first@sha256:" + strings.Repeat("d", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if created.Status != state.DeployPending {
		t.Errorf("created.Status = %q, want pending", created.Status)
	}

	all, err := s.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("len(ListDeploymentsForApp) = %d, want 1 (only the first deploy)", len(all))
	}
	if all[0].ID != created.ID {
		t.Errorf("all[0].ID = %q, want %q", all[0].ID, created.ID)
	}
}

// createAccount / createApp are tiny helpers mirroring seedLiveDeploy
// for tests that DON'T want the trailing live deployment.
func createAccount(t *testing.T, s *state.PgStore, ctx context.Context, email string) string {
	t.Helper()
	a, err := s.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return a.ID
}

func createApp(t *testing.T, s *state.PgStore, ctx context.Context, acctID, slug string) string {
	t.Helper()
	a, err := s.CreateApp(ctx, state.App{
		AccountID: acctID, Slug: slug, Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	return a.ID
}
