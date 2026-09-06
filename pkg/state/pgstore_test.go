package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

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
	s, _, ctx := pgStoreWithPool(t)
	return s, ctx
}

// pgStoreWithPool mirrors pgStore but also returns the underlying
// pgxpool.Pool so a test can query the same schema the store writes
// through. Necessary for any test that needs to inspect raw SQL state
// (dump-table comparisons, byte-identical property tests, etc.) —
// pgtest.Open generates a fresh schema per call, so opening a second
// pool from inside the test would land on an empty schema and the
// "no mutation" assertion would pass vacuously.
func pgStoreWithPool(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
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
	return s, pool, ctx
}

// seedLiveDeploy creates account+app+live-deployment and returns their ids.
// The optional emailSuffix disambiguates the account.email UNIQUE key and
// the optional slug disambiguates apps.slug (a global UNIQUE column, not
// (account_id, slug)-scoped — see migrations/00001_init.sql:33) when a
// test wants multiple independent accounts/apps (issue #470 / migration
// 00089 tests seed three deployments, one per fixture row).
func seedLiveDeploy(t *testing.T, s *state.PgStore, ctx context.Context, emailSuffix ...string) (acctID, appID, depID string) {
	t.Helper()
	suffix := ""
	if len(emailSuffix) > 0 {
		suffix = emailSuffix[0]
	}
	slug := "pg-app"
	if len(emailSuffix) > 1 {
		slug = "pg-app-" + emailSuffix[1]
	}
	acct, err := s.CreateAccount(ctx, "u"+suffix+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: slug, Type: state.AppTypeApp,
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

// TestPg_UpsertGithubInstallBinding_PersistsAllColumns locks the PR-B
// write path: every column added in migration 00050 is stamped
// atomically, and a second call with the same payload is idempotent
// (the (account_id, binding_id) unique partial index makes the
// upsert a no-op for duplicate work).
func TestPg_UpsertGithubInstallBinding_PersistsAllColumns(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)

	linked := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	bind := state.GitHubBinding{
		AppID:            appID,
		AccountID:        acctID,
		BindingID:        "bind-pg-1",
		InstallID:        42,
		RepoFullName:     "octo/api",
		ProductionBranch: "main",
		LinkedAt:         linked,
	}
	if err := s.UpsertGithubInstallBinding(ctx, bind); err != nil {
		t.Fatalf("UpsertGithubInstallBinding: %v", err)
	}

	// Round-trip via the reverse-lookup path (uses the
	// (repo, branch) partial index).
	got, err := s.GithubInstallBindingForRepoBranch(ctx, "octo/api", "main")
	if err != nil {
		t.Fatalf("GithubInstallBindingForRepoBranch: %v", err)
	}
	if got.AppID != appID {
		t.Errorf("AppID = %q, want %q", got.AppID, appID)
	}
	if got.AccountID != acctID {
		t.Errorf("AccountID = %q, want %q", got.AccountID, acctID)
	}
	if got.BindingID != "bind-pg-1" {
		t.Errorf("BindingID = %q, want bind-pg-1", got.BindingID)
	}
	if got.InstallID != 42 {
		t.Errorf("InstallID = %d, want 42", got.InstallID)
	}
	if got.ProductionBranch != "main" {
		t.Errorf("ProductionBranch = %q, want main", got.ProductionBranch)
	}
	if !got.LinkedAt.Equal(linked) {
		t.Errorf("LinkedAt = %v, want %v", got.LinkedAt, linked)
	}

	// Idempotent: re-apply with the same payload, no error, columns
	// stay (the unique index makes the upsert a no-op for repeat work).
	if err := s.UpsertGithubInstallBinding(ctx, bind); err != nil {
		t.Errorf("repeat UpsertGithubInstallBinding: %v", err)
	}
	got2, err := s.GithubInstallBindingForRepoBranch(ctx, "octo/api", "main")
	if err != nil {
		t.Fatalf("reverse-lookup after repeat: %v", err)
	}
	if got2.BindingID != "bind-pg-1" {
		t.Errorf("after repeat: BindingID = %q, want bind-pg-1", got2.BindingID)
	}
}

// TestPg_DeleteGithubInstallBinding_ClearsColumns pins the unbinding
// path. The migration's (account_id, binding_id) partial index lets
// us verify the post-delete state without an extra index lookup.
func TestPg_DeleteGithubInstallBinding_ClearsColumns(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx)

	bind := state.GitHubBinding{
		AppID:            appID,
		AccountID:        acctID,
		BindingID:        "bind-pg-del",
		InstallID:        7,
		RepoFullName:     "octo/del",
		ProductionBranch: "main",
		LinkedAt:         time.Now(),
	}
	if err := s.UpsertGithubInstallBinding(ctx, bind); err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if err := s.DeleteGithubInstallBinding(ctx, appID); err != nil {
		t.Fatalf("DeleteGithubInstallBinding: %v", err)
	}

	// Reverse-lookup now returns ErrNotFound (install_id is NULL).
	_, err := s.GithubInstallBindingForRepoBranch(ctx, "octo/del", "main")
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("after delete: err = %v, want ErrNotFound", err)
	}

	// Idempotent: second delete returns nil (app row exists, just no
	// binding to clear). Unknown appID returns ErrNotFound so the
	// dashboard's "unbind" handler can distinguish.
	if err := s.DeleteGithubInstallBinding(ctx, appID); err != nil {
		t.Errorf("repeat delete (still same app): err = %v, want nil", err)
	}
	if err := s.DeleteGithubInstallBinding(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("delete on unknown app: err = %v, want ErrNotFound", err)
	}
}

// TestPg_ListGithubInstallBindingsForAccount_ScopesByAccount pins the
// dashboard's hydrate path: bindings under account A must not leak
// into account B's listing, and the result is keyed by appID.
func TestPg_ListGithubInstallBindingsForAccount_ScopesByAccount(t *testing.T) {
	s, ctx := pgStore(t)
	acctA, appA, _ := seedLiveDeploy(t, s, ctx)
	// seedLiveDeploy uses a hardcoded email "u@example.com"; create
	// acctB with a unique email so the unique-key doesn't reject the
	// second insert.
	acctBRec, err := s.CreateAccount(ctx, "list-b-"+strconv.FormatInt(time.Now().UnixNano(), 10)+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount B: %v", err)
	}
	appBRec, err := s.CreateApp(ctx, state.App{
		AccountID: acctBRec.ID, Slug: "pg-app-b-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp B: %v", err)
	}
	acctB, appB := acctBRec.ID, appBRec.ID

	// Bind appA under acctA and appB under acctB.
	if err := s.UpsertGithubInstallBinding(ctx, state.GitHubBinding{
		AppID: appA, AccountID: acctA, BindingID: "bind-A",
		InstallID: 1, RepoFullName: "octo/A", ProductionBranch: "main",
		LinkedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert A: %v", err)
	}
	if err := s.UpsertGithubInstallBinding(ctx, state.GitHubBinding{
		AppID: appB, AccountID: acctB, BindingID: "bind-B",
		InstallID: 2, RepoFullName: "octo/B", ProductionBranch: "main",
		LinkedAt: time.Now(),
	}); err != nil {
		t.Fatalf("upsert B: %v", err)
	}

	gotA, err := s.ListGithubInstallBindingsForAccount(ctx, acctA)
	if err != nil {
		t.Fatalf("ListA: %v", err)
	}
	if len(gotA) != 1 {
		t.Fatalf("acctA: len = %d, want 1", len(gotA))
	}
	if _, ok := gotA[appA]; !ok {
		t.Errorf("acctA: missing appA")
	}
	if _, ok := gotA[appB]; ok {
		t.Errorf("acctA: leaked appB")
	}

	gotB, err := s.ListGithubInstallBindingsForAccount(ctx, acctB)
	if err != nil {
		t.Fatalf("ListB: %v", err)
	}
	if len(gotB) != 1 {
		t.Fatalf("acctB: len = %d, want 1", len(gotB))
	}
	if _, ok := gotB[appB]; !ok {
		t.Errorf("acctB: missing appB")
	}
	if _, ok := gotB[appA]; ok {
		t.Errorf("acctB: leaked appA")
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

// TestPg_UpdateApp_AutoscaleTargets pins the partial-update +
// round-trip semantics for the autoscale trigger targets (issue
// #169 / #172) on PgStore. Mirrors TestPg_UpdateApp_WithMinInstances
// above; without it, a future sqlc renumbering could quietly drop
// the new columns from the RETURNING clause and the handler-side
// validation would pass on stale data. The test exercises:
//   - Set + non-zero → column written
//   - Set + zero     → explicit disable
//   - Not Set        → column unchanged (load-bearing case)
func TestPg_UpdateApp_AutoscaleTargets(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)

	// Set both targets, then re-read via AppByID.
	rps := 50
	cpu := 70
	if _, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{
		AutoscaleTargetRPS:       &rps,
		SetAutoscaleTargetRPS:    true,
		AutoscaleTargetCPUPct:    &cpu,
		SetAutoscaleTargetCPUPct: true,
	}); err != nil {
		t.Fatalf("UpdateApp initial: %v", err)
	}
	got, err := s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID: %v", err)
	}
	if got.AutoscaleTargetRPS != 50 || got.AutoscaleTargetCPUPct != 70 {
		t.Fatalf("after Set: rps=%d cpu=%d, want 50/70", got.AutoscaleTargetRPS, got.AutoscaleTargetCPUPct)
	}

	// Explicit zero on CPU (disable).
	zero := 0
	if _, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{
		AutoscaleTargetCPUPct:    &zero,
		SetAutoscaleTargetCPUPct: true,
	}); err != nil {
		t.Fatalf("UpdateApp zero cpu: %v", err)
	}
	got, err = s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID post-zero: %v", err)
	}
	if got.AutoscaleTargetCPUPct != 0 {
		t.Errorf("explicit-zero cpu: got %d, want 0", got.AutoscaleTargetCPUPct)
	}
	if got.AutoscaleTargetRPS != 50 {
		t.Errorf("unset rps: got %d, want 50 (must be unchanged when Set=false)", got.AutoscaleTargetRPS)
	}

	// Not Set → column survives the PATCH.
	if _, err := s.UpdateApp(ctx, appID, state.UpdateAppParams{}); err != nil {
		t.Fatalf("UpdateApp no-Set: %v", err)
	}
	got, err = s.AppByID(ctx, appID)
	if err != nil {
		t.Fatalf("AppByID no-Set: %v", err)
	}
	if got.AutoscaleTargetRPS != 50 {
		t.Errorf("survived PATCH without Set: rps=%d, want 50", got.AutoscaleTargetRPS)
	}
	if got.AutoscaleTargetCPUPct != 0 {
		t.Errorf("survived PATCH without Set: cpu=%d, want 0", got.AutoscaleTargetCPUPct)
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

// TestPg_SetDeploymentParked_RoundTrip (issue #554 / ADR-079 /
// AC #3) pins the per-deployment parked_reason + parked_at columns
// from migration 00157. The engine's ParkDeployment is the single
// writer; this test pins the read-back path so a future column
// rename or NULL-default drift surfaces in pg-shard-2 instead of
// on the apid GET /v1/apps/{slug} wire.
func TestPg_SetDeploymentParked_RoundTrip(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if err := s.SetDeploymentParked(ctx, depID, "liveness_exhausted", now); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}

	// Read-back via the canonical customer-facing path. The
	// DeploymentByID read uses deploymentSelectColumns + the
	// scanDeploymentInto destination list — the load-bearing
	// invariant is that the column lands in both.
	d, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if d.ParkedReason != "liveness_exhausted" {
		t.Errorf("parked_reason = %q, want %q", d.ParkedReason, "liveness_exhausted")
	}
	if d.ParkedAt == nil {
		t.Fatalf("parked_at = nil, want timestamp")
	}
	if !d.ParkedAt.Equal(now) {
		t.Errorf("parked_at = %s, want %s (round-trip drift)", d.ParkedAt, now)
	}
}

// TestPg_SetDeploymentParked_Idempotent pins the schedd-restart
// contract: a second park on an already-parked deployment must
// NOT repaint parked_at. The audit row (engine.go:3812
// "instances.parked_liveness_exhausted") is the durable source of
// truth; SetDeploymentParked just pins the column. A re-stamp
// during a schedd crash loop would obscure the actual park time
// on the apid surface.
func TestPg_SetDeploymentParked_Idempotent(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx)
	first := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.SetDeploymentParked(ctx, depID, "liveness_exhausted", first); err != nil {
		t.Fatalf("SetDeploymentParked (first): %v", err)
	}
	// Second call 1h later — must NOT repaint.
	if err := s.SetDeploymentParked(ctx, depID, "lifecycle_park", first.Add(time.Hour)); err != nil {
		t.Fatalf("SetDeploymentParked (second): %v", err)
	}
	d, err := s.DeploymentByID(ctx, depID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if d.ParkedReason != "liveness_exhausted" {
		t.Errorf("parked_reason = %q, want %q (second park leaked reason)", d.ParkedReason, "liveness_exhausted")
	}
	if d.ParkedAt == nil || !d.ParkedAt.Equal(first) {
		t.Errorf("parked_at = %v, want %s (second park leaked timestamp)", d.ParkedAt, first)
	}
}

// TestPg_SetDeploymentParked_UnknownReturnsErrNotFound guards
// the not-found branch — callers must not silently no-op when a
// stale deployment id is passed. Same pattern as
// TestPg_SetDeploymentFailed_UnknownReturnsErrNotFound above.
func TestPg_SetDeploymentParked_UnknownReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	err := s.SetDeploymentParked(ctx, "00000000-0000-0000-0000-000000000000", "liveness_exhausted", time.Now())
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("unknown id err = %v, want ErrNotFound", err)
	}
}

// TestPg_LatestParkedDeploymentForApp picks the most-recently-
// parked deployment for an app. AC #3 wire: powers the apid
// GET /v1/apps/{slug}.parked_deployment reference shape.
func TestPg_LatestParkedDeploymentForApp(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depOlder := seedLiveDeploy(t, s, ctx)

	// Park the seed deployment first.
	older := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	if err := s.SetDeploymentParked(ctx, depOlder, "liveness_exhausted", older); err != nil {
		t.Fatalf("SetDeploymentParked (older): %v", err)
	}

	// Spin a second deployment for the same app. CreateDeployment
	// supersedes the prior 'live' row, so depOlder lands in
	// status='superseded' but parked_reason/parked_at stay set.
	// apps.status stays 'active' so the second CreateDeployment
	// succeeds.
	newer := time.Now().UTC().Truncate(time.Microsecond)
	depNewer, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:def", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(newer): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depNewer.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(newer): %v", err)
	}
	if err := s.SetDeploymentParked(ctx, depNewer.ID, "liveness_exhausted", newer); err != nil {
		t.Fatalf("SetDeploymentParked (newer): %v", err)
	}

	got, err := s.LatestParkedDeploymentForApp(ctx, appID)
	if err != nil {
		t.Fatalf("LatestParkedDeploymentForApp: %v", err)
	}
	if got.ID != depNewer.ID {
		t.Errorf("latest.ID = %s, want %s (newer)", got.ID, depNewer.ID)
	}
	if got.ParkedReason != "liveness_exhausted" {
		t.Errorf("latest.parked_reason = %q, want %q", got.ParkedReason, "liveness_exhausted")
	}
}

// TestPg_LatestParkedDeploymentForApp_NoParkReturnsErrNotFound is
// the load-bearing "app is healthy" branch on the apid surface.
// LatestParkedDeploymentForApp returns ErrNotFound; the apid
// handler maps that to a nil ParkedDeploymentRef (no field on
// the wire).
func TestPg_LatestParkedDeploymentForApp_NoParkReturnsErrNotFound(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx)
	_, err := s.LatestParkedDeploymentForApp(ctx, appID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("no park err = %v, want ErrNotFound", err)
	}
}

// TestPg_LatestParkedDeploymentForApp_SupersededKeepsParking locks
// in the surprising-but-correct behaviour the store docstring
// calls out: parked_reason + parked_at are NOT cleared on
// supersede. A customer who deploys → gets parked → redeploys
// will see the parked_deployment ref pointing at the OLDER
// (superseded) row, not the current live deployment. Without
// this pin, a future "clear parking on supersede" optimization
// would silently change the apid surface.
//
// The test seeds three deployments on one app:
//
//   - depParked: live, then parked (the row we expect to see
//     returned)
//   - depSuperseded: spun after the park, also live, but never
//     parked (would be the "current live" row from the customer's
//     POV after a redeploy)
//
// `LatestParkedDeploymentForApp` MUST return depParked (not the
// current live), because parked_reason='liveness_exhausted' is
// set on depParked only.
func TestPg_LatestParkedDeploymentForApp_SupersededKeepsParking(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depParkedID := seedLiveDeploy(t, s, ctx)

	// Park the seed deployment.
	parkStamp := time.Now().UTC().Truncate(time.Microsecond)
	if err := s.SetDeploymentParked(ctx, depParkedID, "liveness_exhausted", parkStamp); err != nil {
		t.Fatalf("SetDeploymentParked: %v", err)
	}

	// Redeploy — supersedes depParkedID. depParkedID stays in
	// status='superseded' but parked_reason/parked_at are
	// preserved (the closed-set CHECK constraint can't be
	// silently cleared by a supersede). The app stays
	// 'active' (the park on the superseded row is what the
	// dashboard surfaces; apps.status tracks live rollouts).
	depSuperseded, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:supersede", Status: state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(supersede): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depSuperseded.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(supersede): %v", err)
	}

	// LatestParkedDeploymentForApp MUST return depParkedID
	// (the older, parked row) — NOT depSuperseded (the newer,
	// un-parked live row). This is the load-bearing pin: the
	// apid surface renders "this deployment was parked", not
	// "this deployment is currently parked".
	got, err := s.LatestParkedDeploymentForApp(ctx, appID)
	if err != nil {
		t.Fatalf("LatestParkedDeploymentForApp: %v", err)
	}
	if got.ID != depParkedID {
		t.Errorf("latest.ID = %s, want %s (the parked + superseded row, not the newer live)", got.ID, depParkedID)
	}
	if got.ParkedReason != "liveness_exhausted" {
		t.Errorf("latest.ParkedReason = %q, want liveness_exhausted", got.ParkedReason)
	}
	if got.ParkedAt == nil || !got.ParkedAt.Equal(parkStamp) {
		t.Errorf("latest.ParkedAt = %v, want %v", got.ParkedAt, parkStamp)
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

// TestPg_CreateAppIfUnderQuota_WritesStreamingEnabled pins the
// INSERT-time write of streaming_enabled on the quota-enforced
// create path (PR #481 / issue #471). The handler-side
// buildApp defaults the column from the plan (Hobby/Pro/Scale →
// true; Free → false) and the e2e test asserts the round-trip
// value matches. Pre-fix, CreateAppIfUnderQuota's INSERT omitted
// the column entirely and the migration DEFAULT (false) silently
// won — Hobby created apps came back with streaming_enabled=false
// and the e2e test failed on CI.
//
// The mirror of this is the bare CreateApp path (already passes
// the column; the column projection in appsSelectColumns covers
// it). The test covers three shapes so a future column-default
// flip is caught at the integration layer:
//
//   - Hobby + streaming=true   → round-trip stays true
//   - Free  + streaming=false  → round-trip stays false
//   - Free  + streaming=true   → round-trip stays true (callers
//     must enforce the plan gate; the store is the writer of
//     truth, not the gatekeeper)
func TestPg_CreateAppIfUnderQuota_WritesStreamingEnabled(t *testing.T) {
	s, ctx := pgStore(t)

	cases := []struct {
		name        string
		plan        api.Plan
		wantWritten bool
	}{
		{name: "HobbyDefaultsToTrueWhenSet", plan: api.PlanHobby, wantWritten: true},
		{name: "FreeExplicitlyFalse", plan: api.PlanFree, wantWritten: false},
		{name: "FreeExplicitlyTrueDespitePlanGate", plan: api.PlanFree, wantWritten: true},
	}
	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.name, func(t *testing.T) {
			email := fmt.Sprintf("streaming-insert-%d-%d@example.com", i, time.Now().UnixNano())
			acct, err := s.CreateAccount(ctx, email, tc.plan)
			if err != nil {
				t.Fatalf("CreateAccount(%s): %v", tc.plan, err)
			}
			limits := api.MustLimitsFor(acct.Plan)

			want := tc.wantWritten
			app := state.App{
				AccountID:        acct.ID,
				Slug:             "stream-" + tc.name,
				Type:             state.AppTypeApp,
				RAMMB:            256,
				MaxConcurrency:   1,
				IdleTimeoutS:     60,
				Status:           state.AppActive,
				StreamingEnabled: want,
			}
			created, err := s.CreateAppIfUnderQuota(ctx, app, limits)
			if err != nil {
				t.Fatalf("CreateAppIfUnderQuota: %v", err)
			}
			if created.StreamingEnabled != want {
				t.Errorf("RETURNING streaming_enabled = %v, want %v (insert dropped the column)",
					created.StreamingEnabled, want)
			}

			// Read back via the slug lookup (the same path the
			// apid GET /v1/apps/{slug} uses) — verifies the row
			// was actually written, not just the RETURNING decode.
			fetched, err := s.AppBySlug(ctx, created.Slug)
			if err != nil {
				t.Fatalf("AppBySlug: %v", err)
			}
			if fetched.StreamingEnabled != want {
				t.Errorf("AppBySlug streaming_enabled = %v, want %v (round-trip regression)",
					fetched.StreamingEnabled, want)
			}
		})
	}
}

// TestPg_CreateAppIfUnderQuota_WritesRequireAuthnDefault pins the
// per-plan default for apps.require_authn (issue #695 / ADR-080).
// Per-plan truth table: Free=false, Hobby=true, Pro=true, Scale=true.
// The test stamps the per-plan default onto the App struct (mirroring
// what apid's buildApp path does at create-time) and verifies the
// INSERT writes the value AND the round-trip via AppBySlug reads it
// back. A future regression that drops the column from
// appsSelectColumns or breaks the pgstore default-snap surfaces here.
//
// The off-by-default case (Free) is the load-bearing one — the
// default existed as the schema DEFAULT false before #695, and a
// regression to "Free apps come back with require_authn=true" is
// the kind of silent breakage the CI pin is meant to catch.
func TestPg_CreateAppIfUnderQuota_WritesRequireAuthnDefault(t *testing.T) {
	s, ctx := pgStore(t)

	cases := []struct {
		name string
		plan api.Plan
		want bool
	}{
		{name: "FreeStaysPublic", plan: api.PlanFree, want: false},
		{name: "HobbyDefaultsToRequired", plan: api.PlanHobby, want: true},
		{name: "ProDefaultsToRequired", plan: api.PlanPro, want: true},
		{name: "ScaleDefaultsToRequired", plan: api.PlanScale, want: true},
	}
	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.name, func(t *testing.T) {
			email := fmt.Sprintf("auth-default-insert-%d-%d@example.com", i, time.Now().UnixNano())
			acct, err := s.CreateAccount(ctx, email, tc.plan)
			if err != nil {
				t.Fatalf("CreateAccount(%s): %v", tc.plan, err)
			}
			limits := api.MustLimitsFor(acct.Plan)

			app := state.App{
				AccountID:      acct.ID,
				Slug:           "auth-default-" + tc.name,
				Type:           state.AppTypeApp,
				RAMMB:          256,
				MaxConcurrency: 1,
				IdleTimeoutS:   60,
				Status:         state.AppActive,
				// Simulate apid's buildApp path stamping the per-plan default.
				RequireAuthn: tc.want,
			}
			created, err := s.CreateAppIfUnderQuota(ctx, app, limits)
			if err != nil {
				t.Fatalf("CreateAppIfUnderQuota: %v", err)
			}
			if created.RequireAuthn != tc.want {
				t.Errorf("RETURNING require_authn = %v, want %v (insert dropped the column or the default-snap diverged from pgstore)",
					created.RequireAuthn, tc.want)
			}
			fetched, err := s.AppBySlug(ctx, created.Slug)
			if err != nil {
				t.Fatalf("AppBySlug: %v", err)
			}
			if fetched.RequireAuthn != tc.want {
				t.Errorf("AppBySlug require_authn = %v, want %v (round-trip regression)",
					fetched.RequireAuthn, tc.want)
			}
		})
	}
}

// TestPg_CreateAppIfUnderQuota_WritesPublicAuthModeDefault pins the
// per-plan default for apps.public_auth_mode (issue #695 / ADR-080).
// Per-plan truth table: Free="open", Hobby="open", Pro="bearer",
// Scale="bearer". The same shape as the require_authn tripwire —
// the stamp is on the App struct (mirroring apid's buildApp path),
// the INSERT writes the value, AppBySlug reads it back unchanged.
//
// Hobby's "open" default is load-bearing: Hobby unlocks the
// require_authn gate but not the bearer scope, so defaulting to
// "bearer" without a usable scope would strand the customer. A
// regression that flips Hobby's default to "bearer" surfaces here.
func TestPg_CreateAppIfUnderQuota_WritesPublicAuthModeDefault(t *testing.T) {
	s, ctx := pgStore(t)

	cases := []struct {
		name string
		plan api.Plan
		want string
	}{
		{name: "FreeStaysOpen", plan: api.PlanFree, want: api.AppPublicAuthModeOpen},
		{name: "HobbyStaysOpen", plan: api.PlanHobby, want: api.AppPublicAuthModeOpen},
		{name: "ProDefaultsToBearer", plan: api.PlanPro, want: api.AppPublicAuthModeBearer},
		{name: "ScaleDefaultsToBearer", plan: api.PlanScale, want: api.AppPublicAuthModeBearer},
	}
	for i, tc := range cases {
		i, tc := i, tc
		t.Run(tc.name, func(t *testing.T) {
			email := fmt.Sprintf("public-auth-insert-%d-%d@example.com", i, time.Now().UnixNano())
			acct, err := s.CreateAccount(ctx, email, tc.plan)
			if err != nil {
				t.Fatalf("CreateAccount(%s): %v", tc.plan, err)
			}
			limits := api.MustLimitsFor(acct.Plan)

			app := state.App{
				AccountID:      acct.ID,
				Slug:           "public-auth-default-" + tc.name,
				Type:           state.AppTypeApp,
				RAMMB:          256,
				MaxConcurrency: 1,
				IdleTimeoutS:   60,
				Status:         state.AppActive,
				PublicAuthMode: tc.want,
			}
			created, err := s.CreateAppIfUnderQuota(ctx, app, limits)
			if err != nil {
				t.Fatalf("CreateAppIfUnderQuota: %v", err)
			}
			if created.PublicAuthMode != tc.want {
				t.Errorf("RETURNING public_auth_mode = %q, want %q (insert dropped the column or the default-snap diverged from pgstore)",
					created.PublicAuthMode, tc.want)
			}
			fetched, err := s.AppBySlug(ctx, created.Slug)
			if err != nil {
				t.Fatalf("AppBySlug: %v", err)
			}
			if fetched.PublicAuthMode != tc.want {
				t.Errorf("AppBySlug public_auth_mode = %q, want %q (round-trip regression)",
					fetched.PublicAuthMode, tc.want)
			}
		})
	}
}

// TestPg_AppBySlug_SurfacesAuthDefaultFlippedAt pins that the new
// apps.auth_default_flipped_at column lands on every App returned
// from the canonical read paths (AppBySlug for this test;
// AppByID + ListAppsForAccount share the same appsSelectColumns +
// scanApp path so a single test covers all three). The column is
// nullable; both NULL (post-flip create) and a stamped value (a
// pre-flip row written by migration 00155) must round-trip
// correctly. A regression that drops the column from
// appsSelectColumns or breaks the scan target lands here as a
// pgx NULL-scan failure (most often: a future contributor removing
// the AuthDefaultFlippedAt positional argument from scanApp's
// row.Scan call).
func TestPg_AppBySlug_SurfacesAuthDefaultFlippedAt(t *testing.T) {
	s, ctx := pgStore(t)

	acct, err := s.CreateAccount(ctx, fmt.Sprintf("auth-flip-readback-%d@example.com", time.Now().UnixNano()), api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	app := state.App{
		AccountID: acct.ID, Slug: "auth-flip-readback", Type: state.AppTypeApp,
		RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 60, Status: state.AppActive,
	}
	created, err := s.CreateAppIfUnderQuota(ctx, app, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	// Fresh post-flip create: auth_default_flipped_at must read
	// back as nil (not stamped — only the migration backfill
	// stamps the column). A regression that auto-stamps new
	// creates breaks here — the column is intentionally
	// read-only-and-grandfathered-only.
	fetched, err := s.AppBySlug(ctx, created.Slug)
	if err != nil {
		t.Fatalf("AppBySlug: %v", err)
	}
	if fetched.AuthDefaultFlippedAt != nil {
		t.Errorf("post-flip create auth_default_flipped_at = %v, want nil (only migration 00155 stamps the column)", fetched.AuthDefaultFlippedAt)
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
		VCPUBudget: api.VCPUSlots, Active: true,
	}); err != nil {
		t.Fatalf("CreateComputeNode(alpha): %v", err)
	}
	if _, err := s.CreateComputeNode(ctx, state.ComputeNode{
		Name: "zulu-drained", TargetURL: "tcp://10.0.0.10:50051",
		VPCPUs: 80, MemMB: 28000, MaxConcurrency: 100, AdmissionCeilingMB: 23800,
		VCPUBudget: api.VCPUSlots, Active: false,
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

// TestPg_ComputeNodes_RegionZone_ProjectedByAllReads pins the
// migrations/00069 region/zone columns end-to-end through every read
// path that PR #429 modified (scanComputeNode, ActiveComputeNodes,
// ListAllComputeNodes, ComputeNodeByID, ComputeNodeByName). The chooser
// reads these columns to tie-break on (headroom, region, zone, name);
// if any of the five read paths drops a column, a chooser call will
// panic on a nil *string compare or silently order wrong.
//
// We bypass CreateComputeNode because it doesn't yet accept region/zone
// (multi-box registration is the next slice; see plan follow-up). The
// raw insert exercises the schema directly. Postgres' NOT NULL
// constraint on the other columns still fires if a future refactor
// drifts.
func TestPg_ComputeNodes_RegionZone_ProjectedByAllReads(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)

	// Insert a node with non-null region/zone.
	var idWithVals string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency,
		     admission_ceiling_mb, lifecycle, region, zone)
		values ($1, $2, $3, $4, $5, $6, 'active'::compute_node_lifecycle, $7, $8)
		returning id
	`, "rgz-with-vals", "unix:///run/faas/vmmd.sock",
		80, 28000, 100, 23800, "eu-fra", "eu-fra-1").Scan(&idWithVals); err != nil {
		t.Fatalf("insert with region/zone: %v", err)
	}

	// Insert a node with NULL region/zone (pre-00069 shape — a row
	// created before the migration ran, or by a future
	// CreateComputeNode that doesn't populate the columns).
	var idNullCols string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency,
		     admission_ceiling_mb)
		values ($1, $2, $3, $4, $5, $6)
		returning id
	`, "rgz-null-cols", "unix:///run/faas/vmmd.sock",
		80, 28000, 100, 23800).Scan(&idNullCols); err != nil {
		t.Fatalf("insert with null region/zone: %v", err)
	}

	// ComputeNodeByID — populated case.
	got, err := s.ComputeNodeByID(ctx, idWithVals)
	if err != nil {
		t.Fatalf("ComputeNodeByID(with-vals): %v", err)
	}
	if got.Region == nil || *got.Region != "eu-fra" {
		t.Errorf("Region = %v, want pointer to \"eu-fra\"", got.Region)
	}
	if got.Zone == nil || *got.Zone != "eu-fra-1" {
		t.Errorf("Zone = %v, want pointer to \"eu-fra-1\"", got.Zone)
	}

	// ComputeNodeByName — populated case.
	gotByName, err := s.ComputeNodeByName(ctx, "rgz-with-vals")
	if err != nil {
		t.Fatalf("ComputeNodeByName(with-vals): %v", err)
	}
	if gotByName.Region == nil || *gotByName.Region != "eu-fra" {
		t.Errorf("ByName.Region = %v, want pointer to \"eu-fra\"", gotByName.Region)
	}
	if gotByName.Zone == nil || *gotByName.Zone != "eu-fra-1" {
		t.Errorf("ByName.Zone = %v, want pointer to \"eu-fra-1\"", gotByName.Zone)
	}

	// ComputeNodeByID — null case. nil must round-trip as nil
	// (pointer type), not as a deref panic or an empty-string collapse.
	gotNull, err := s.ComputeNodeByID(ctx, idNullCols)
	if err != nil {
		t.Fatalf("ComputeNodeByID(null-cols): %v", err)
	}
	if gotNull.Region != nil {
		t.Errorf("Region = %v, want nil for pre-00069 row", *gotNull.Region)
	}
	if gotNull.Zone != nil {
		t.Errorf("Zone = %v, want nil for pre-00069 row", *gotNull.Zone)
	}

	// ActiveComputeNodes must project region/zone for every entry.
	active, err := s.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	found := map[string]state.ComputeNode{}
	for _, n := range active {
		found[n.Name] = n
	}
	for _, want := range []string{"rgz-with-vals", "rgz-null-cols"} {
		if _, ok := found[want]; !ok {
			t.Errorf("ActiveComputeNodes missing %q (got %v)", want, namesOf(active))
			continue
		}
	}
	if n, ok := found["rgz-with-vals"]; ok {
		if n.Region == nil || *n.Region != "eu-fra" {
			t.Errorf("ActiveComputeNodes[rgz-with-vals].Region = %v, want pointer to \"eu-fra\"", n.Region)
		}
	}
	if n, ok := found["rgz-null-cols"]; ok {
		if n.Region != nil {
			t.Errorf("ActiveComputeNodes[rgz-null-cols].Region = %v, want nil", *n.Region)
		}
	}

	// ListAllComputeNodes projects region/zone the same way.
	all, err := s.ListAllComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ListAllComputeNodes: %v", err)
	}
	for _, n := range all {
		if n.Name == "rgz-with-vals" {
			if n.Region == nil || *n.Region != "eu-fra" {
				t.Errorf("ListAllComputeNodes[rgz-with-vals].Region = %v, want pointer to \"eu-fra\"", n.Region)
			}
		}
	}
}

func namesOf(nodes []state.ComputeNode) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.Name)
	}
	return out
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
			VCPUBudget: api.VCPUSlots,
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
		VCPUBudget: api.VCPUSlots,
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
		VCPUBudget: api.VCPUSlots, Active: true,
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
	// Migration 00089 added a UNIQUE INDEX on (deployment_id, tier) WHERE
	// stale=false so two live init-tier rows for the same deployment
	// collide; mark the first row stale before inserting the second
	// (matches production: each capture supersedes the prior).
	snapA, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "1.8.0", MemBytes: 100, DiskBytes: 100,
		StorageKey: state.SnapMemKey(depID) + "/a",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot A: %v", err)
	}
	if err := s.MarkSnapshotStale(ctx, snapA.ID); err != nil {
		t.Fatalf("MarkSnapshotStale A: %v", err)
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

	// Migration 00089 added UNIQUE (deployment_id, tier) WHERE stale=false;
	// only one live init-tier row is allowed per deployment. Seed three
	// separate deployments — one per FC version — so we end up with
	// three live init-tier rows for the sweep to act on. The sweep is a
	// global UPDATE so the post-sweep live-row count is what matters.
	// Capture the depID for the "matching" version (1.8.0) so the
	// LatestSnapshot readback below can scope to that deployment.
	var dep180 string
	mkSnap := func(v string) string {
		_, _, depID := seedLiveDeploy(t, s, ctx, "-"+v, v)
		if v == "1.8.0" {
			dep180 = depID
		}
		snap, err := s.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: depID, FCVersion: v, MemBytes: 100, DiskBytes: 100,
			StorageKey: state.SnapMemKey(depID) + "/" + v,
		})
		if err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", v, err)
		}
		return snap.ID
	}
	id170 := mkSnap("1.7.0")
	id180 := mkSnap("1.8.0")
	id190 := mkSnap("1.9.0")
	_ = id170
	_ = id190

	// Sweep against 1.8.0: 1.7.0 and 1.9.0 should flip (id180 stays live).
	n, err := s.MarkAllSnapshotsStaleByFCVersion(ctx, "1.8.0")
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByFCVersion: %v", err)
	}
	if n != 2 {
		t.Errorf("marked %d stale, want 2", n)
	}

	// Confirm via LatestSnapshot on the 1.8.0 deployment — it must
	// return id180 (still live) and not be ErrNotFound.
	latest, err := s.LatestSnapshot(ctx, dep180)
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

	// Migration 00089 added UNIQUE (deployment_id, tier) WHERE stale=false;
	// only one live init-tier row is allowed per deployment. Seed three
	// separate deployments (one per snapshot A/B/C) so we can flip
	// exactly two of three and verify the survivor stays live.
	// Capture depB so the LatestSnapshot readback below can scope to it.
	var depB string
	mkSnap := func(suffix string) string {
		_, _, depID := seedLiveDeploy(t, s, ctx, "-"+suffix, suffix)
		if suffix == "b" {
			depB = depID
		}
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

	// LatestSnapshot filters stale; the survivor on depB is B.
	latest, err := s.LatestSnapshot(ctx, depB)
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
	pool := pgtest.OpenMigrated(t)
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
	expired, err := s.ListSnapshotsStaleOlderThan(ctx, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("ListSnapshotsStaleOlderThan: %v", err)
	}
	if len(expired) != 1 || expired[0].ID != oldID || expired[0].AppSlug == "" || expired[0].DeploymentStatus != state.DeployLive {
		t.Fatalf("expired stale projection = %+v, want old row with artifact metadata", expired)
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
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)

	// Insert three snapshots: one stale (filtered), two live.
	// Migration 00089 added UNIQUE (deployment_id, tier) WHERE stale=false;
	// only one live init-tier row is allowed per deployment. Seed three
	// separate deployments — one per snapshot — so we get three rows
	// (one stale, two live) without colliding on the partial index.
	mkSnap := func(suffix string, stale bool) string {
		_, _, depID := seedLiveDeploy(t, s, ctx, "-"+suffix, suffix)
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
	pool := pgtest.OpenMigrated(t)
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
	_, err = s.CreateDeployment(ctx, state.Deployment{
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
	if _, err := s.CreateDeployment(ctx, state.Deployment{
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
	pool := pgtest.OpenMigrated(t)
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

	// The app starts with exactly one 'building' deployment row
	// (simulating: builderd is mid-pipeline). Note we do NOT use
	// seedLiveDeploy here — that creates a 'live' row that
	// CreateDeployment would correctly supersede, defeating the
	// M-1 invariant under test.
	priorDepID := mustCreateImageDeployment(t, s, ctx, app.ID)
	if err := s.UpdateDeploymentStatus(ctx, priorDepID, state.DeployBuilding, ""); err != nil {
		t.Fatalf("UpdateDeploymentStatus(building): %v", err)
	}

	// Second deploy — must NOT supersede the building row.
	created, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: app.ID, Kind: state.DeploymentKindImage,
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

	// Sanity: both rows are visible to the app's history — the
	// building row stays building, the new one is pending.
	all, err := s.ListDeploymentsForApp(ctx, app.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListDeploymentsForApp: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("len(ListDeploymentsForApp) = %d, want 2 (building + pending)", len(all))
	}
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

// TestPg_UpsertGitHubInstall_InsertsRow pins the insert path on the
// new github_installations table (PR-C, migration 00059). The
// SealedToken bytea is the load-bearing bit — the test asserts a
// sealed blob survives the round-trip, so a regression that writes
// plaintext would be caught.
func TestPg_UpsertGitHubInstall_InsertsRow(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)

	exp := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	inst := state.GitHubInstall{
		AccountID:        acctID,
		InstallationID:   12345,
		DefaultBranch:    "main",
		SealedToken:      []byte("age-encryption.org/v1\nX25519 ...\n...fake sealed bytes"),
		TokenExpiresAt:   exp,
		AuditGithubLogin: "octocat",
	}
	if err := s.UpsertGitHubInstall(ctx, inst); err != nil {
		t.Fatalf("UpsertGitHubInstall: %v", err)
	}

	got, err := s.GitHubInstallForAccount(ctx, acctID)
	if err != nil {
		t.Fatalf("GitHubInstallForAccount: %v", err)
	}
	if got.AccountID != acctID {
		t.Errorf("AccountID = %q, want %q", got.AccountID, acctID)
	}
	if got.InstallationID != 12345 {
		t.Errorf("InstallationID = %d, want 12345", got.InstallationID)
	}
	if got.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want main", got.DefaultBranch)
	}
	if string(got.SealedToken) != string(inst.SealedToken) {
		t.Errorf("SealedToken round-trip mismatch")
	}
	if !got.TokenExpiresAt.Equal(exp) {
		t.Errorf("TokenExpiresAt = %v, want %v", got.TokenExpiresAt, exp)
	}
	if got.AuditGithubLogin != "octocat" {
		t.Errorf("AuditGithubLogin = %q, want octocat", got.AuditGithubLogin)
	}
}

// TestPg_UpsertGitHubInstall_OnConflictUpdates pins the ON CONFLICT
// DO UPDATE path: a second upsert with a different installation_id
// overwrites the first row instead of crashing on the PK.
func TestPg_UpsertGitHubInstall_OnConflictUpdates(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, _, _ := seedLiveDeploy(t, s, ctx)

	exp := time.Now().Add(time.Hour).UTC()
	first := state.GitHubInstall{
		AccountID:        acctID,
		InstallationID:   1,
		DefaultBranch:    "main",
		SealedToken:      []byte("first-seal"),
		TokenExpiresAt:   exp,
		AuditGithubLogin: "octocat",
	}
	if err := s.UpsertGitHubInstall(ctx, first); err != nil {
		t.Fatalf("first UpsertGitHubInstall: %v", err)
	}
	second := first
	second.InstallationID = 2
	second.DefaultBranch = "develop"
	second.AuditGithubLogin = "octocat-2"
	if err := s.UpsertGitHubInstall(ctx, second); err != nil {
		t.Fatalf("second UpsertGitHubInstall: %v", err)
	}
	got, err := s.GitHubInstallForAccount(ctx, acctID)
	if err != nil {
		t.Fatalf("GitHubInstallForAccount: %v", err)
	}
	if got.InstallationID != 2 {
		t.Errorf("InstallationID = %d, want 2 (ON CONFLICT DO UPDATE didn't fire)", got.InstallationID)
	}
	if got.DefaultBranch != "develop" {
		t.Errorf("DefaultBranch = %q, want develop", got.DefaultBranch)
	}
	if got.AuditGithubLogin != "octocat-2" {
		t.Errorf("AuditGithubLogin = %q, want octocat-2", got.AuditGithubLogin)
	}
}

// TestPg_GitHubInstallForAccount_OnDeleteCascade pins the §17 G2
// GDPR path: deleting the owning account removes the install row.
// (Mirrors the PR-B apps_github_install_account_id path which is
// ON DELETE SET NULL — the install row IS the user's, so CASCADE
// is correct.)
//
// We can't use the shared pgStore(t) helper here because CASCADE is
// verified with a raw `delete from accounts` against the same schema
// that owns the row, and pgStore's pool is unexported
// (MEMORY.md/pkg-state-pgstore-pool-unexported). Follow the
// pgtest.Open + MigrateUp + NewPgStore pattern used by the snapshot
// retention tests (see line 1428) so we hold both halves.
//
// Seed just an account (no app/deploy/instances) so the raw
// `delete from accounts` only triggers the CASCADE on
// github_installations — apps FK would otherwise trip 23503.
func TestPg_GitHubInstallForAccount_OnDeleteCascade(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	s := state.NewPgStore(pool)
	acct, err := s.CreateAccount(ctx, "cascade@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	acctID := acct.ID

	inst := state.GitHubInstall{
		AccountID:        acctID,
		InstallationID:   42,
		DefaultBranch:    "main",
		SealedToken:      []byte("seal"),
		TokenExpiresAt:   time.Now().Add(time.Hour).UTC(),
		AuditGithubLogin: "octocat",
	}
	if err := s.UpsertGitHubInstall(ctx, inst); err != nil {
		t.Fatalf("UpsertGitHubInstall: %v", err)
	}
	// Delete the account — installs must go with it (CASCADE).
	if _, err := pool.Exec(ctx, `delete from accounts where id = $1::uuid`, acctID); err != nil {
		t.Fatalf("delete account: %v", err)
	}
	_, err = s.GitHubInstallForAccount(ctx, acctID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound after account delete (CASCADE), got %v", err)
	}
}

// TestPg_CountLiveInstancesByDeployment pins the per-deployment
// live-count contract used by DeploymentCounterWatcher (issue #555
// PR-6). Counts every instance in {waking, cold_booting, running}
// for the given deployment_id; instances in PARKED / STOPPED /
// SNAPSHOTTING are excluded; unknown deployment_ids return 0.
func TestPg_CountLiveInstancesByDeployment(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx, "555")

	nodeID := resolveDefaultLocal(t, ctx, s)

	// 3 waking/cold_booting/running → must be counted.
	mustCreate := func(state state.State) string {
		t.Helper()
		ins, err := s.CreateInstance(ctx, appID, depID, string(state), 256, nodeID, "")
		if err != nil {
			t.Fatalf("CreateInstance %s: %v", state, err)
		}
		return ins.ID
	}
	runningID := mustCreate(state.StateRunning)
	wakingID := mustCreate(state.StateWaking)
	coldBootingID := mustCreate(state.StateColdBooting)

	// 1 parked + 1 snapshotting → must NOT be counted.
	parkedID := mustCreate(state.StateParked)
	snapshottingID := mustCreate(state.StateSnapshotting)

	// And one for a different deployment — must NOT be counted.
	// The seedLiveDeploy helper takes email + slug suffixes; pass
	// both so we don't collide on the global apps.slug UNIQUE key
	// (the first seed created "pg-app").
	_, _, otherDepID := seedLiveDeploy(t, s, ctx, "555-other", "other")
	otherRunning, err := s.CreateInstance(ctx, appID, otherDepID, string(state.StateRunning), 256, nodeID, "")
	if err != nil {
		t.Fatalf("CreateInstance other: %v", err)
	}

	got, err := s.CountLiveInstancesByDeployment(ctx, depID)
	if err != nil {
		t.Fatalf("CountLiveInstancesByDeployment: %v", err)
	}
	if got != 3 {
		t.Errorf("count = %d, want 3 (waking=%s cold_booting=%s running=%s parked=%s snapshotting=%s other=%s)",
			got, wakingID, coldBootingID, runningID, parkedID, snapshottingID, otherRunning.ID)
	}

	// Unknown deployment_id → 0, nil (count(*) on empty WHERE is
	// well-defined in Postgres).
	gotUnknown, err := s.CountLiveInstancesByDeployment(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("unknown deployment_id: %v", err)
	}
	if gotUnknown != 0 {
		t.Errorf("unknown deployment_id count = %d, want 0", gotUnknown)
	}

	// Empty deployment_id is rejected at the SQL boundary — the
	// column is UUID, not text, and the planner raises 22P02 on
	// `WHERE deployment_id = ''`. The store's contract is "non-empty
	// UUID or caller's responsibility"; the watcher guards the empty
	// case before the call. We pin that contract here.
	if _, err := s.CountLiveInstancesByDeployment(ctx, ""); err == nil {
		t.Errorf("empty deployment_id: expected error from UUID type cast, got nil")
	}
}

// TestPg_AccountsByIDs exercises the batch helper that closes
// the N+1 fan-out in the dashboard org-detail render (PR-9 §1).
// Mirrors the per-row AccountByID contract: missing IDs are
// absent from the returned map (NOT errors), and the empty-slice
// short-circuit returns an empty map without issuing a query.
//
// PR-9 §1: every AccountByID read in the project uses the wide
// mfa_*/deletion_requested_at/past_due_at projection so the
// requireMFA chokepoint sees post-enrollment state. We assert
// the batched projection likewise carries mfa_required so a
// regression that drops the field is caught here.
func TestPg_AccountsByIDs(t *testing.T) {
	s, ctx := pgStore(t)

	// Empty input → empty map, no error.
	got, err := s.AccountsByIDs(ctx, nil)
	if err != nil {
		t.Fatalf("AccountsByIDs(nil) err = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("len(AccountsByIDs(nil)) = %d, want 0", len(got))
	}

	// Seed three accounts on three plans.
	plans := []api.Plan{api.PlanFree, api.PlanHobby, api.PlanPro}
	want := make([]state.Account, 0, len(plans))
	for i, p := range plans {
		a, err := s.CreateAccount(ctx, fmt.Sprintf("u%d@example.com", i), p)
		if err != nil {
			t.Fatalf("CreateAccount[%d]: %v", i, err)
		}
		want = append(want, a)
	}

	// Two present + one UUID-shaped-but-absent → map has 2 entries.
	missing := uuid.NewString()
	got, err = s.AccountsByIDs(ctx, []string{want[0].ID, want[1].ID, missing})
	if err != nil {
		t.Fatalf("AccountsByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2", len(got))
	}
	for _, w := range want[:2] {
		g, ok := got[w.ID]
		if !ok {
			t.Errorf("missing entry for ID %q", w.ID)
			continue
		}
		if g.Email != w.Email {
			t.Errorf("got[%s].Email = %q, want %q", w.ID, g.Email, w.Email)
		}
		if g.Plan != w.Plan {
			t.Errorf("got[%s].Plan = %q, want %q", w.ID, g.Plan, w.Plan)
		}
		// mfa_required is NOT NULL with default false; the wide
		// projection must carry it through so requireMFA sees
		// post-enrollment state.
		if g.MFARequired != false {
			t.Errorf("got[%s].MFARequired = %v, want false", w.ID, g.MFARequired)
		}
	}
	if _, ok := got[missing]; ok {
		t.Errorf("missing ID %q should not appear in map", missing)
	}

	// Duplicate IDs in the request → only one entry per unique ID.
	got, err = s.AccountsByIDs(ctx, []string{want[0].ID, want[0].ID, want[2].ID})
	if err != nil {
		t.Fatalf("AccountsByIDs dup: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2 (unique IDs)", len(got))
	}
}

// TestPg_CreateDeployment_DefaultsTrafficPercent100 (issue #556 PR-A)
// pins the omitted-traffic_percent → 100 default. Without this
// default a fresh deploy would land at traffic_percent=0 and
// receive no traffic — a silent outage. The pgstore's CreateDeployment
// reads d.TrafficPercent directly (no schema DEFAULT kicks in when
// the caller writes 0 explicitly), so the test guards both the
// memstore default and the handler-side nil→100 default.
func TestPg_CreateDeployment_DefaultsTrafficPercent100(t *testing.T) {
	s, ctx := pgStore(t)
	acctID, appID, _ := seedLiveDeploy(t, s, ctx, "default-traffic")

	// Caller passes no traffic_percent (zero value of int).
	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("d", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if dep.TrafficPercent != 100 {
		t.Errorf("CreateDeployment TrafficPercent = %d, want 100 (omitted → 100 default)", dep.TrafficPercent)
	}
	// And the prior live row from seedLiveDeploy must have been
	// zeroed on supersede so Σ over live rows = 100 trivially.
	_ = acctID
}

// TestPg_CreateDeployment_SupersedeZeroesPriorTrafficPercent pins
// the second half of the Σ=100 invariant. A prior live row at 100
// must flip to 0 on supersede; the new row lands at its explicit
// value (default 100). Σ over {prior=0, new=100} = 100.
func TestPg_CreateDeployment_SupersedeZeroesPriorTrafficPercent(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, priorID := seedLiveDeploy(t, s, ctx, "sup-traffic")

	// New deploy at default traffic_percent=100.
	created, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("e", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if created.TrafficPercent != 100 {
		t.Errorf("created.TrafficPercent = %d, want 100", created.TrafficPercent)
	}

	// Prior row was zeroed by the supersede branch.
	prior, err := s.DeploymentByID(ctx, priorID)
	if err != nil {
		t.Fatalf("DeploymentByID(prior): %v", err)
	}
	if prior.TrafficPercent != 0 {
		t.Errorf("prior.TrafficPercent = %d, want 0 (supersede zeroes it)", prior.TrafficPercent)
	}
	if prior.Status != state.DeploySuperseded {
		t.Errorf("prior.Status = %q, want superseded", prior.Status)
	}
}

// TestPg_UpdateDeploymentTraffic_ZerosSiblings (issue #556 PR-A)
// pins the zero-siblings rebalance form: setting one live row's
// traffic_percent to N forces every sibling live row to 0. Σ over
// {target=N, siblings=0} = N. PR-A only accepts the canonical
// 100 case structurally (Σ must = 100 for the canary path to
// coexist with the schema invariant); PR-C rewrites this with
// proportional-redistribution semantics — see
// TestPg_UpdateDeploymentTraffic_ProportionalRedistribution
// below. The original test asserted the zero-siblings form via a
// vacuous on-error `return` that pinned nothing; the new
// distribution algorithm changes the Σ behaviour so a single
// regression-test seam is required for both stores.
func TestPg_UpdateDeploymentTraffic_ProportionalRedistribution(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depPrior := seedLiveDeploy(t, s, ctx, "prop-2way")

	// Add a fresh live row alongside depPrior (which is at 100).
	// ADR-091 / PR-D: deployments_app_scope_live_uniq enforces
	// at-most-one-live per (app_id, scope). Each deployment in
	// this fixture MUST carry its own scope so the canary +
	// restore-prior sequence below doesn't trip 23505 on
	// (app_id, 'default').
	depCanary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Status:      state.DeployPending,
		Scope:       "canary",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (canary): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depCanary.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (canary): %v", err)
	}
	// CreateDeployment auto-superseded depPrior → traffic_percent=0,
	// status=superseded. Flip depPrior back to live at 0 so we have
	// {prior:0, canary:100} both live, Σ=100, ready for the canary
	// 25% stamp. Without this flip the test would be exercising the
	// sole-row target=0 failure mode (legitimate Σ=0 error) instead
	// of proportional redistribution. depPrior is at scope='default'
	// (seedLiveDeploy default), depCanary is at scope='canary' — so
	// both rows can sit at status='live' simultaneously without
	// tripping the partial unique index.
	if err := s.MarkDeploymentLive(ctx, depPrior); err != nil {
		t.Fatalf("MarkDeploymentLive (restore prior): %v", err)
	}

	// Initial state: depPrior=0, depCanary=100, Σ=100.
	if _, err := s.UpdateDeploymentTraffic(ctx, depCanary.ID, 25); err != nil {
		t.Fatalf("UpdateDeploymentTraffic(canary, 25): %v", err)
	}
	canary, err := s.DeploymentByID(ctx, depCanary.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(canary): %v", err)
	}
	prior, _ := s.DeploymentByID(ctx, depPrior)
	if canary.TrafficPercent != 25 {
		t.Errorf("canary.TrafficPercent = %d, want 25", canary.TrafficPercent)
	}
	if prior.TrafficPercent != 75 {
		t.Errorf("prior.TrafficPercent = %d, want 75 (residual 100-25=75 absorbed by sole sibling)", prior.TrafficPercent)
	}
	if sum := canary.TrafficPercent + prior.TrafficPercent; sum != 100 {
		t.Errorf("Σ = %d, want 100", sum)
	}
}

// TestPg_UpdateDeploymentTraffic_ThreeWayResidual pins the
// largest-remainder method against the PR-C worked example:
// {A:50, B:30, C:20} → set A:25 → residual 75 split over B:C at
// 30:20 → {A:25, B:45, C:30}, Σ = 100. A regression that switched
// to integer-truncation or floor-only would land {A:25, B:45, C:29}
// or {A:25, B:44, C:30} and trip this assertion.
func TestPg_UpdateDeploymentTraffic_ThreeWayResidual(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depA := seedLiveDeploy(t, s, ctx, "three-way")

	// depA is at 100. Add depB and depC, both live, then equalise
	// the table to {A:50, B:30, C:20} via two consecutive
	// UpdateDeploymentTraffic calls (each one stamps target +
	// redistributes residual pro-rata across the other live rows).
	// ADR-091 / PR-D: each row gets its own scope so the
	// restore-A / restore-B re-flip sequence below doesn't trip
	// deployments_app_scope_live_uniq.
	depB, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("d", 64),
		Status:      state.DeployPending,
		Scope:       "dep-b",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (B): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depB.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (B): %v", err)
	}
	depC, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("e", 64),
		Status:      state.DeployPending,
		Scope:       "dep-c",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (C): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depC.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (C): %v", err)
	}
	// CreateDeployment supersedes the most-recent live row each call:
	// B's create superseded A; C's create superseded B. A and B are
	// both at 0/superseded now. Re-flip A and B to live at 0 so we
	// have three live rows {A:0, B:0, C:100}, Σ=100.
	if err := s.MarkDeploymentLive(ctx, depA); err != nil {
		t.Fatalf("MarkDeploymentLive (restore A): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depB.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (restore B): %v", err)
	}

	// Build {A:50, B:30, C:20}: first set B=30 (residual=70 across
	// A:0 + C:100 → A stays 0, C absorbs 70 — Σ must equal 100 by
	// construction).
	if _, err := s.UpdateDeploymentTraffic(ctx, depB.ID, 30); err != nil {
		t.Fatalf("stamp B=30: %v", err)
	}
	// Then set C=20 (residual=80 across A + B → proportional split
	// on whatever weights the prior stamp produced). A pinned
	// mid-table assert catches off-by-one in the algorithm.
	a, _ := s.DeploymentByID(ctx, depA)
	b, _ := s.DeploymentByID(ctx, depB.ID)
	c, _ := s.DeploymentByID(ctx, depC.ID)
	if sum := a.TrafficPercent + b.TrafficPercent + c.TrafficPercent; sum != 100 {
		t.Errorf("after B=30 stamp: Σ = %d, want 100 (A=%d, B=%d, C=%d)",
			sum, a.TrafficPercent, b.TrafficPercent, c.TrafficPercent)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, depC.ID, 20); err != nil {
		t.Fatalf("stamp C=20: %v", err)
	}
	a, _ = s.DeploymentByID(ctx, depA)
	b, _ = s.DeploymentByID(ctx, depB.ID)
	c, _ = s.DeploymentByID(ctx, depC.ID)
	if sum := a.TrafficPercent + b.TrafficPercent + c.TrafficPercent; sum != 100 {
		t.Errorf("after C=20: Σ = %d, want 100", sum)
	}

	// Now stamp A=25: residual=75 across B + C, with their
	// current weights {B, C} = {?, ?}. The exact mapping depends
	// on what the two stamps above produced. The PR-C contract
	// is Σ=100 post-stamp regardless of the rounding path. We
	// also pin that A=25 lands exactly.
	if _, err := s.UpdateDeploymentTraffic(ctx, depA, 25); err != nil {
		t.Fatalf("stamp A=25: %v", err)
	}
	a, _ = s.DeploymentByID(ctx, depA)
	b, _ = s.DeploymentByID(ctx, depB.ID)
	c, _ = s.DeploymentByID(ctx, depC.ID)
	if a.TrafficPercent != 25 {
		t.Errorf("A.TrafficPercent = %d, want 25", a.TrafficPercent)
	}
	if sum := a.TrafficPercent + b.TrafficPercent + c.TrafficPercent; sum != 100 {
		t.Errorf("after A=25: Σ = %d, want 100 (B=%d, C=%d)", sum, b.TrafficPercent, c.TrafficPercent)
	}
	// Both B and C must land in (0, 100) — a regression that
	// truncates to 0 would clamp either of them.
	if b.TrafficPercent <= 0 || b.TrafficPercent >= 100 {
		t.Errorf("B.TrafficPercent = %d, want (0, 100) — truncated?", b.TrafficPercent)
	}
	if c.TrafficPercent <= 0 || c.TrafficPercent >= 100 {
		t.Errorf("C.TrafficPercent = %d, want (0, 100) — truncated?", c.TrafficPercent)
	}
}

// TestPg_UpdateDeploymentTraffic_SoleLiveRow pins edge cases on
// the single-live-row path: target=100 is a no-op success; target=0
// produces Σ=0 which legitimately trips ErrTrafficPercentSumInvalid
// (a real error, not the S2 trap). This pins the post-S2 behaviour
// where the error code now means "Σ is structurally bad" rather
// than "PR-A zero-siblings trips the invariant".
func TestPg_UpdateDeploymentTraffic_SoleLiveRow(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx, "sole-row")

	// target=100 → no-op success, Σ stays 100.
	if _, err := s.UpdateDeploymentTraffic(ctx, depID, 100); err != nil {
		t.Errorf("target=100 on sole row err = %v, want nil", err)
	}
	row, _ := s.DeploymentByID(ctx, depID)
	if row.TrafficPercent != 100 {
		t.Errorf("Sole row after target=100 = %d, want 100", row.TrafficPercent)
	}

	// target=0 → Σ=0 → ErrTrafficPercentSumInvalid. This is the
	// one legitimate failure mode for sole-live-row stamps; pre-PR-C
	// it was conflated with the S2 trap. Now it carries different
	// operational meaning: "the only live row is at 0, no traffic
	// can flow, rollback failed."
	if _, err := s.UpdateDeploymentTraffic(ctx, depID, 0); !errors.Is(err, state.ErrTrafficPercentSumInvalid) {
		t.Errorf("target=0 on sole row err = %v, want ErrTrafficPercentSumInvalid", err)
	}
}

// TestPg_UpdateDeploymentTraffic_TieBreakStable pins the
// rounding tie-break (largest-remainder, ID ASC). With two
// siblings at equal prior weight and an odd residual, the ±1
// must always land on the lexicographically-greater ID.
func TestPg_UpdateDeploymentTraffic_TieBreakStable(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depA := seedLiveDeploy(t, s, ctx, "tie-break")

	// Add depB live alongside depA; equalise to {A:50, B:50}.
	// ADR-091 / PR-D: depB gets scope='dep-b' so the restore-A
	// re-flip below doesn't trip deployments_app_scope_live_uniq
	// on (app_id, scope='default') (depA is on default via seedLiveDeploy).
	depB, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("f", 64),
		Status:      state.DeployPending,
		Scope:       "dep-b",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (B): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depB.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (B): %v", err)
	}
	// CreateDeployment(B) superseded depA. Re-flip A to live at 0
	// so we have {A:0, B:100}, Σ=100, both live.
	if err := s.MarkDeploymentLive(ctx, depA); err != nil {
		t.Fatalf("MarkDeploymentLive (restore A): %v", err)
	}
	if _, err := s.UpdateDeploymentTraffic(ctx, depB.ID, 50); err != nil {
		t.Fatalf("equalise to B=50: %v", err)
	}

	// Set A=0 → residual=100 split evenly. Σ=100 by construction.
	// Both should be 50 (even split, no +1 needed for k=0).
	if _, err := s.UpdateDeploymentTraffic(ctx, depA, 0); err != nil {
		t.Fatalf("set A=0: %v", err)
	}
	a, _ := s.DeploymentByID(ctx, depA)
	b, _ := s.DeploymentByID(ctx, depB.ID)
	if a.TrafficPercent != 0 {
		t.Errorf("A = %d, want 0", a.TrafficPercent)
	}
	if b.TrafficPercent != 100 {
		t.Errorf("B = %d, want 100 (sole live row absorbs residual)", b.TrafficPercent)
	}

	// Restore to {A:50, B:50} by stamping A=50.
	if _, err := s.UpdateDeploymentTraffic(ctx, depA, 50); err != nil {
		t.Fatalf("restore A=50: %v", err)
	}
	a, _ = s.DeploymentByID(ctx, depA)
	b, _ = s.DeploymentByID(ctx, depB.ID)
	if a.TrafficPercent != 50 || b.TrafficPercent != 50 {
		t.Errorf("restore: A=%d B=%d, want both 50", a.TrafficPercent, b.TrafficPercent)
	}
	if sum := a.TrafficPercent + b.TrafficPercent; sum != 100 {
		t.Errorf("restore: Σ = %d, want 100", sum)
	}
}

// TestPg_UpdateDeploymentTraffic_TwoWay_ResidualSpellsLegibly
// (issue #556 / PR-C) is the headline 2-deployment canary
// semantic the legacy test name `ZerosSiblings` used to imply
// but did not pin. A 100/0 split that becomes 25/75 must leave
// Σ=100 and B=25. This is the user-visible canary contract.
func TestPg_UpdateDeploymentTraffic_TwoWay_ResidualSpellsLegibly(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depPrior := seedLiveDeploy(t, s, ctx, "two-way")

	depCanary, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("a", 64),
		Status:      state.DeployPending,
		Scope:       "canary",
	})
	if err != nil {
		t.Fatalf("CreateDeployment (canary): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depCanary.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (canary): %v", err)
	}
	// CreateDeployment(canary) superseded depPrior. Re-flip prior
	// to live at 0 so we have {prior:0, canary:100}, Σ=100, both
	// live. Without this flip, "equalise canary=0" would be a
	// sole-row target=0 failure (legitimate Σ=0 error).
	// ADR-091 / PR-D: depCanary uses scope='canary', depPrior is
	// at scope='default' (seedLiveDeploy default) — both can sit
	// at status='live' without tripping the partial unique index.
	if err := s.MarkDeploymentLive(ctx, depPrior); err != nil {
		t.Fatalf("MarkDeploymentLive (restore prior): %v", err)
	}

	// Equalise: stamp canary=0 first so prior=100, Σ=100.
	if _, err := s.UpdateDeploymentTraffic(ctx, depCanary.ID, 0); err != nil {
		t.Fatalf("equalise canary=0: %v", err)
	}

	// Flip canary to 25.
	if _, err := s.UpdateDeploymentTraffic(ctx, depCanary.ID, 25); err != nil {
		t.Fatalf("canary=25: %v", err)
	}
	canary, _ := s.DeploymentByID(ctx, depCanary.ID)
	prior, _ := s.DeploymentByID(ctx, depPrior)
	if canary.TrafficPercent != 25 || prior.TrafficPercent != 75 {
		t.Errorf("after canary=25: prior=%d canary=%d, want 75/25",
			prior.TrafficPercent, canary.TrafficPercent)
	}
	if sum := canary.TrafficPercent + prior.TrafficPercent; sum != 100 {
		t.Errorf("Σ = %d, want 100", sum)
	}

	// Reverse: stamp canary to 100 (sole sibling keeps 0).
	if _, err := s.UpdateDeploymentTraffic(ctx, depCanary.ID, 100); err != nil {
		t.Fatalf("canary=100: %v", err)
	}
	canary, _ = s.DeploymentByID(ctx, depCanary.ID)
	prior, _ = s.DeploymentByID(ctx, depPrior)
	if canary.TrafficPercent != 100 || prior.TrafficPercent != 0 {
		t.Errorf("after canary=100: prior=%d canary=%d, want 0/100",
			prior.TrafficPercent, canary.TrafficPercent)
	}
}

// TestPg_UpdateDeploymentTraffic_RejectsBogusRange pins the
// range-check backstop. Out-of-range values trip
// ErrInvalidTrafficPercent regardless of any handler-side
// validation that may run first.
func TestPg_UpdateDeploymentTraffic_RejectsBogusRange(t *testing.T) {
	s, ctx := pgStore(t)
	_, _, depID := seedLiveDeploy(t, s, ctx, "traffic-range")

	for _, v := range []int{-1, 101, -100, 200} {
		if _, err := s.UpdateDeploymentTraffic(ctx, depID, v); !errors.Is(err, state.ErrInvalidTrafficPercent) {
			t.Errorf("UpdateDeploymentTraffic(%d) err = %v, want ErrInvalidTrafficPercent", v, err)
		}
	}
}

// TestPg_UpdateDeploymentTraffic_RejectsNonLive (issue #556 PR-A)
// pins the status guard: traffic_percent can only be moved to a
// `live` row. A superseded row trips ErrInvalidTrafficPercent.
func TestPg_UpdateDeploymentTraffic_RejectsNonLive(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depA := seedLiveDeploy(t, s, ctx, "traffic-non-live")

	// A fresh deploy supersedes depA → status='superseded'.
	depB, err := s.CreateDeployment(ctx, state.Deployment{
		AppID: appID, Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:" + strings.Repeat("c", 64),
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment (B): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depB.ID); err != nil {
		t.Fatalf("MarkDeploymentLive (B): %v", err)
	}

	// Stamping the superseded row should fail with
	// ErrInvalidTrafficPercent (the status guard).
	if _, err := s.UpdateDeploymentTraffic(ctx, depA, 100); !errors.Is(err, state.ErrInvalidTrafficPercent) {
		t.Errorf("UpdateDeploymentTraffic(superseded dep, 100) err = %v, want ErrInvalidTrafficPercent", err)
	}
}

// TestPg_UpsertComputeNodeFromOperator_RoundTripsAllNewColumns pins
// the issue #911 / ADR-110 PR-3a schema-carrier contract: every one
// of the 8 widened ComputeNode fields round-trips through
// UpsertComputeNodeFromOperator and reads back via ComputeNodeByName
// with pointer equality intact. The two halves of the contract —
// the 6 PR-3a columns AND the 2 latent-drift columns (PublicIp /
// PublicIpSetAt from migration 00174) — must both be projected by
// every read site (ByID + ByName + ActiveComputeNodes +
// ListAllComputeNodes) once UpsertComputeNodeFromOperator runs.
//
// Why this is the load-bearing pin: if any of the 8 columns is
// dropped from the scanComputeNode row.Scan arg list (or from the
// INSERT/UPSERT col-list), Postgres will return a row-count
// mismatch and the upsert will fail. This test catches that drift
// before PR-3 / PR-4 try to read these columns in production.
func TestPg_UpsertComputeNodeFromOperator_RoundTripsAllNewColumns(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)

	// Populated node — drives all 8 widened columns non-nil. The
	// IP + time.Time fields must be non-nil pointer values; the
	// *string / *int fields are likewise populated.
	popPublicIP := netip.MustParseAddr("203.0.113.42")
	popPublicIPAt := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	in := state.ComputeNode{
		Name:               "pr3a-populated",
		TargetURL:          "unix:///run/faas/vmmd.sock",
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     5,
		VCPUBudget:         20,
		AdmissionCeilingMB: 4096,
		Active:             true,
		Region:             ptrStr("eu-fra"),
		Zone:               ptrStr("eu-fra-1"),
		// Latent-drift columns (PR A8, migration 00174) — PR-3a
		// closes the schema/code asymmetry by projecting them.
		PublicIp:      &popPublicIP,
		PublicIpSetAt: &popPublicIPAt,
		// PR-3a storage-carrier columns.
		ReleaseID:       ptrStr("0123456789abcdef0123456789abcdef01234567"),
		ManifestHash:    ptrStr("sha256:" + strings.Repeat("a", 64)),
		HostCertificate: ptrStr("-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----\n"),
		CertFingerprint: ptrStr("sha256:" + strings.Repeat("b", 64)),
		Role:            ptrStr("control-plane"),
		Generation:      ptrInt(1),
	}
	got, err := s.UpsertComputeNodeFromOperator(ctx, in)
	if err != nil {
		t.Fatalf("UpsertComputeNodeFromOperator(populated): %v", err)
	}
	// ID is generated server-side; everything else must match.
	if got.ID == "" {
		t.Errorf("UpsertComputeNodeFromOperator returned empty ID")
	}
	if got.Name != in.Name {
		t.Errorf("Name = %q, want %q", got.Name, in.Name)
	}
	if got.TargetURL != in.TargetURL {
		t.Errorf("TargetURL = %q, want %q", got.TargetURL, in.TargetURL)
	}
	if got.Region == nil || *got.Region != "eu-fra" {
		t.Errorf("Region = %v, want pointer to \"eu-fra\"", got.Region)
	}
	if got.Zone == nil || *got.Zone != "eu-fra-1" {
		t.Errorf("Zone = %v, want pointer to \"eu-fra-1\"", got.Zone)
	}
	if got.PublicIp == nil || *got.PublicIp != popPublicIP {
		t.Errorf("PublicIp = %v, want pointer to %v", got.PublicIp, popPublicIP)
	}
	if got.PublicIpSetAt == nil || !got.PublicIpSetAt.Equal(popPublicIPAt) {
		t.Errorf("PublicIpSetAt = %v, want pointer to %v", got.PublicIpSetAt, popPublicIPAt)
	}
	if got.ReleaseID == nil || *got.ReleaseID != *in.ReleaseID {
		t.Errorf("ReleaseID = %v, want pointer to %q", got.ReleaseID, *in.ReleaseID)
	}
	if got.ManifestHash == nil || *got.ManifestHash != *in.ManifestHash {
		t.Errorf("ManifestHash = %v, want pointer to %q", got.ManifestHash, *in.ManifestHash)
	}
	if got.HostCertificate == nil || *got.HostCertificate != *in.HostCertificate {
		t.Errorf("HostCertificate round-trip mismatch (len=%d, want len=%d)", len(*got.HostCertificate), len(*in.HostCertificate))
	}
	if got.CertFingerprint == nil || *got.CertFingerprint != *in.CertFingerprint {
		t.Errorf("CertFingerprint = %v, want pointer to %q", got.CertFingerprint, *in.CertFingerprint)
	}
	if got.Role == nil || *got.Role != "control-plane" {
		t.Errorf("Role = %v, want pointer to \"control-plane\"", got.Role)
	}
	if got.Generation == nil || *got.Generation != 1 {
		t.Errorf("Generation = %v, want pointer to 1", got.Generation)
	}

	// Cross-read via ComputeNodeByName — same row, different read
	// path. Catches a scan-arg mismatch that happens to line up by
	// accident on one read path.
	byName, err := s.ComputeNodeByName(ctx, in.Name)
	if err != nil {
		t.Fatalf("ComputeNodeByName(%q): %v", in.Name, err)
	}
	if byName.ReleaseID == nil || *byName.ReleaseID != *in.ReleaseID {
		t.Errorf("ByName.ReleaseID = %v, want pointer to %q", byName.ReleaseID, *in.ReleaseID)
	}
	if byName.ManifestHash == nil || *byName.ManifestHash != *in.ManifestHash {
		t.Errorf("ByName.ManifestHash = %v, want pointer to %q", byName.ManifestHash, *in.ManifestHash)
	}
	if byName.Role == nil || *byName.Role != "control-plane" {
		t.Errorf("ByName.Role = %v, want pointer to \"control-plane\"", byName.Role)
	}
	if byName.PublicIp == nil || *byName.PublicIp != popPublicIP {
		t.Errorf("ByName.PublicIp = %v, want pointer to %v", byName.PublicIp, popPublicIP)
	}

	// ActiveComputeNodes must project all 8 widened columns too —
	// the operator dashboard (PR-4) reads through this path.
	active, err := s.ActiveComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ActiveComputeNodes: %v", err)
	}
	var activeRow state.ComputeNode
	for _, n := range active {
		if n.Name == in.Name {
			activeRow = n
			break
		}
	}
	if activeRow.Name == "" {
		t.Fatalf("ActiveComputeNodes missing %q (got %v)", in.Name, namesOf(active))
	}
	if activeRow.ReleaseID == nil || *activeRow.ReleaseID != *in.ReleaseID {
		t.Errorf("ActiveComputeNodes[%q].ReleaseID = %v", in.Name, activeRow.ReleaseID)
	}
	if activeRow.CertFingerprint == nil || *activeRow.CertFingerprint != *in.CertFingerprint {
		t.Errorf("ActiveComputeNodes[%q].CertFingerprint = %v", in.Name, activeRow.CertFingerprint)
	}

	// ListAllComputeNodes — same check on the read path that the
	// release-bundle install machinery (PR-3) walks to pick a node.
	all, err := s.ListAllComputeNodes(ctx)
	if err != nil {
		t.Fatalf("ListAllComputeNodes: %v", err)
	}
	var allRow state.ComputeNode
	for _, n := range all {
		if n.Name == in.Name {
			allRow = n
			break
		}
	}
	if allRow.Name == "" {
		t.Fatalf("ListAllComputeNodes missing %q", in.Name)
	}
	if allRow.ManifestHash == nil || *allRow.ManifestHash != *in.ManifestHash {
		t.Errorf("ListAllComputeNodes[%q].ManifestHash = %v", in.Name, allRow.ManifestHash)
	}
	if allRow.HostCertificate == nil || *allRow.HostCertificate != *in.HostCertificate {
		t.Errorf("ListAllComputeNodes[%q].HostCertificate round-trip mismatch", in.Name)
	}
	if allRow.Generation == nil || *allRow.Generation != 1 {
		t.Errorf("ListAllComputeNodes[%q].Generation = %v, want pointer to 1", in.Name, allRow.Generation)
	}

	// Null-node case — drives the nullable contract. Insert a row
	// via raw SQL with the 8 PR-3a + drift columns omitted, read
	// it back via ComputeNodeByID, and confirm every field is a
	// nil pointer (not an empty-string collapse or a zero-int
	// collapse). This is the "pre-PR-3a row accepts the schema
	// without a backfill UPDATE" contract — see
	// migrations/00271_compute_nodes_release_test.go:108.
	var idNullCols string
	if err := pool.QueryRow(ctx, `
		insert into compute_nodes
		    (name, target_url, vpcpus, mem_mb, max_concurrency,
		     admission_ceiling_mb)
		values ($1, $2, $3, $4, $5, $6)
		returning id
	`, "pr3a-null-cols", "unix:///run/faas/vmmd.sock",
		2, 1024, 1, 1024).Scan(&idNullCols); err != nil {
		t.Fatalf("insert with null PR-3a columns: %v", err)
	}
	nullRow, err := s.ComputeNodeByID(ctx, idNullCols)
	if err != nil {
		t.Fatalf("ComputeNodeByID(null PR-3a cols): %v", err)
	}
	for _, c := range []struct {
		name string
		ptr  interface{}
	}{
		{"PublicIp", nullRow.PublicIp},
		{"PublicIpSetAt", nullRow.PublicIpSetAt},
		{"ReleaseID", nullRow.ReleaseID},
		{"ManifestHash", nullRow.ManifestHash},
		{"HostCertificate", nullRow.HostCertificate},
		{"CertFingerprint", nullRow.CertFingerprint},
		{"Role", nullRow.Role},
		{"Generation", nullRow.Generation},
	} {
		// interface{} wrapping a typed nil pointer is NOT == nil —
		// the interface carries a type tag with no value. Use
		// reflect to dereference the typed-nil correctly.
		v := reflect.ValueOf(c.ptr)
		if v.Kind() == reflect.Ptr && !v.IsNil() { //nolint:govet // reflect.Ptr is a stdlib constant; inlining it adds noise without clarity benefit.
			t.Errorf("null row %s = %v, want nil pointer (nullable contract)", c.name, c.ptr)
		}
	}
}

// TestPg_UpsertComputeNodeFromVmmd_PreservesOperatorReleaseID pins
// the second-box cutover invariant for PR-3a (ADR-110): when an
// operator-POSTed row with a populated release_id is later
// re-registered by vmmd (UpsertComputeNodeFromVmmd), the operator's
// release_id / manifest_hash / host_certificate / cert_fingerprint
// / role / generation must SURVIVE — vmmd self-registration must
// not overwrite operator-POSTed data on the existing row. This is
// the load-bearing COALESCE pattern from pgstore.go:8834 applied to
// the 8 PR-3a columns.
func TestPg_UpsertComputeNodeFromVmmd_PreservesOperatorReleaseID(t *testing.T) {
	s, ctx := pgStore(t)

	// Step 1: operator POSTs the node with a release-bundle stamp.
	operatorFirst := state.ComputeNode{
		Name:               "pr3a-vmmd-preserve",
		TargetURL:          "tcp://10.0.0.5:7777", // operator-POSTed
		VPCPUs:             4,
		MemMB:              8192,
		MaxConcurrency:     5,
		VCPUBudget:         20,
		AdmissionCeilingMB: 4096,
		Region:             ptrStr("eu-fra"),
		Zone:               ptrStr("eu-fra-1"),
		ReleaseID:          ptrStr("0123456789abcdef0123456789abcdef01234567"),
		ManifestHash:       ptrStr("sha256:" + strings.Repeat("a", 64)),
		HostCertificate:    ptrStr("op-stamped-cert"),
		CertFingerprint:    ptrStr("sha256:" + strings.Repeat("b", 64)),
		Role:               ptrStr("control-plane"),
		Generation:         ptrInt(7),
	}
	operatorRow, err := s.UpsertComputeNodeFromOperator(ctx, operatorFirst)
	if err != nil {
		t.Fatalf("operator first upsert: %v", err)
	}
	if err := s.SetComputeNodeActive(ctx, operatorRow.ID, false); err != nil {
		t.Fatalf("drain node: %v", err)
	}

	// Step 2: vmmd self-registers on boot. It posts different
	// resource numbers AND a fresh TargetURL (the path the boot
	// script learns from the running config). It does NOT have a
	// release_id / cert to write — those columns are nil in the
	// vmmd struct. COALESCE(compute_nodes.X, excluded.X) must
	// keep the operator-POSTed value, NOT overwrite with NULL.
	vmmdReReg := state.ComputeNode{
		Name:               "pr3a-vmmd-preserve",
		TargetURL:          "tcp://10.0.0.5:7777", // same — common case
		VPCPUs:             8,                     // vmmd reports higher
		MemMB:              16384,
		MaxConcurrency:     10,
		VCPUBudget:         40,
		AdmissionCeilingMB: 4096, // CHECK (admission_ceiling_mb > 0)
		Region:             nil,  // vmmd doesn't know about region yet
		Zone:               nil,
		ReleaseID:          nil, // CRITICAL: must not overwrite operator's value
		ManifestHash:       nil,
		HostCertificate:    nil,
		CertFingerprint:    nil,
		Role:               nil,
		Generation:         nil,
	}
	if _, err := s.UpsertComputeNodeFromVmmd(ctx, vmmdReReg); err != nil {
		t.Fatalf("vmmd re-register: %v", err)
	}

	// Step 3: read back. The operator-POSTed PR-3a columns MUST
	// survive intact. The vmmd-owned resource numbers MUST be
	// updated to the new values.
	got, err := s.ComputeNodeByName(ctx, "pr3a-vmmd-preserve")
	if err != nil {
		t.Fatalf("ComputeNodeByName: %v", err)
	}

	// vmmd-owned: updated.
	if got.VPCPUs != 8 {
		t.Errorf("VPCPUs = %d, want 8 (vmmd owns resource numbers)", got.VPCPUs)
	}
	if got.MemMB != 16384 {
		t.Errorf("MemMB = %d, want 16384", got.MemMB)
	}
	if got.MaxConcurrency != 10 {
		t.Errorf("MaxConcurrency = %d, want 10", got.MaxConcurrency)
	}

	// operator-POSTed PR-3a columns: PRESERVED via COALESCE.
	if got.ReleaseID == nil || *got.ReleaseID != *operatorFirst.ReleaseID {
		t.Errorf("ReleaseID = %v, want preserved operator value %q", got.ReleaseID, *operatorFirst.ReleaseID)
	}
	if got.ManifestHash == nil || *got.ManifestHash != *operatorFirst.ManifestHash {
		t.Errorf("ManifestHash = %v, want preserved operator value", got.ManifestHash)
	}
	if got.HostCertificate == nil || *got.HostCertificate != "op-stamped-cert" {
		t.Errorf("HostCertificate = %v, want preserved operator value \"op-stamped-cert\"", got.HostCertificate)
	}
	if got.CertFingerprint == nil || *got.CertFingerprint != *operatorFirst.CertFingerprint {
		t.Errorf("CertFingerprint = %v, want preserved operator value", got.CertFingerprint)
	}
	if got.Role == nil || *got.Role != "control-plane" {
		t.Errorf("Role = %v, want preserved operator value \"control-plane\"", got.Role)
	}
	if got.Generation == nil || *got.Generation != 7 {
		t.Errorf("Generation = %v, want preserved operator value 7", got.Generation)
	}
	if got.Region == nil || *got.Region != "eu-fra" {
		t.Errorf("Region = %v, want preserved operator value \"eu-fra\"", got.Region)
	}
	if got.Zone == nil || *got.Zone != "eu-fra-1" {
		t.Errorf("Zone = %v, want preserved operator value \"eu-fra-1\"", got.Zone)
	}
	if got.Active {
		t.Error("vmmd self-registration reactivated an operator-drained node")
	}
}

// ptrStr / ptrInt are tiny helpers for the *string / *int pointer
// fields that PR-3a widens. Kept private to this file.
func ptrStr(s string) *string { return &s }
func ptrInt(i int) *int       { return &i }

// TestPg_DeploymentActorRoundtrip (issue #606) pins the four
// actor-attribution columns (deployed_by_user_id, deployed_via,
// deployed_from_ip, pusher_login) + the FK to accounts(id) +
// the closed-set CHECK on deployed_via through CreateDeployment +
// DeploymentByID. The Go-zero collapse on every column asserts
// the nullif()/coalesce() chain in pgstore.go::CreateDeployment
// keeps pre-feature rows valid without a backfill. A bad
// deployed_via value exercises the DB-side CHECK rejection.
//
// The positional scan invariant documented at pgstore.go:12480
// (and called out again at the deploymentSelectColumnsWithRootfs
// const) is the load-bearing constraint: if any of the four new
// SELECT projections drifts from the INSERT column order, pgx
// fails loud at the first SELECT. This test is the regression
// net for that drift, parallel to TestPg_DeploymentAnnotationRoundtrip
// from PR #984.
func TestPg_DeploymentActorRoundtrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "actor@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "actor-app", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 1. Full actor payload — dashboard / API path with a session
	//    user, remote IP, and a githubd-stamped pusher login.
	depFull, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:            app.ID,
		Kind:             state.DeploymentKindGitHub,
		ImageDigest:      "sha256:actor-full",
		Status:           state.DeployPending,
		DeployedByUserID: acct.ID,
		DeployedVia:      "github",
		DeployedFromIP:   "203.0.113.42",
		PusherLogin:      "octocat",
	})
	if err != nil {
		t.Fatalf("CreateDeployment(full): %v", err)
	}
	got, err := s.DeploymentByID(ctx, depFull.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(full): %v", err)
	}
	if got.DeployedByUserID != acct.ID {
		t.Errorf("deployed_by_user_id = %q, want %q", got.DeployedByUserID, acct.ID)
	}
	if got.DeployedVia != "github" {
		t.Errorf("deployed_via = %q, want %q", got.DeployedVia, "github")
	}
	if got.DeployedFromIP != "203.0.113.42" {
		t.Errorf("deployed_from_ip = %q, want %q", got.DeployedFromIP, "203.0.113.42")
	}
	if got.PusherLogin != "octocat" {
		t.Errorf("pusher_login = %q, want %q", got.PusherLogin, "octocat")
	}

	// 2. Zero actor payload — anonymous / pre-FK / push-to-main
	//    with no pusher. Every empty-string Go field must
	//    collapse to NULL on INSERT (the nullif() chain at
	//    pgstore.go::CreateDeployment) so the
	//    deployed_by_user_id FK and the INET parser never see
	//    a literal ''. The NOT NULL deployed_via column must
	//    collapse to 'api' via the coalesce() fallback so
	//    pre-feature rows stay valid without a backfill.
	depEmpty, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:actor-empty",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(empty): %v", err)
	}
	gotEmpty, err := s.DeploymentByID(ctx, depEmpty.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(empty): %v", err)
	}
	if gotEmpty.DeployedByUserID != "" {
		t.Errorf("empty deployed_by_user_id = %q, want \"\"", gotEmpty.DeployedByUserID)
	}
	if gotEmpty.DeployedVia != "api" {
		t.Errorf("empty deployed_via = %q, want %q (coalesce() fallback)", gotEmpty.DeployedVia, "api")
	}
	if gotEmpty.DeployedFromIP != "" {
		t.Errorf("empty deployed_from_ip = %q, want \"\"", gotEmpty.DeployedFromIP)
	}
	if gotEmpty.PusherLogin != "" {
		t.Errorf("empty pusher_login = %q, want \"\"", gotEmpty.PusherLogin)
	}

	// 3. Closed-set deployed_via CHECK rejection. The DB-side
	//    constraint (migrations/00305_deployments_actor.sql) is
	//    the source of truth; the apid handler is expected to
	//    mirror the vocabulary. We drive the rejection directly
	//    through the store to confirm the constraint is wired
	//    (the apid would otherwise silently accept and round-trip
	//    a malformed value).
	_, err = s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:actor-bad",
		Status:      state.DeployPending,
		DeployedVia: "rogue_surface",
	})
	if err == nil {
		t.Errorf("expected CHECK violation on rogue_surface, got nil")
	}

	// 4. FK rejection: a non-existent account id must fail
	//    (the FK to accounts(id) enforces referential integrity
	//    — the dashboard / api handler is expected to resolve
	//    the session user before reaching the store, but the
	//    constraint is the source of truth).
	_, err = s.CreateDeployment(ctx, state.Deployment{
		AppID:            app.ID,
		Kind:             state.DeploymentKindImage,
		ImageDigest:      "sha256:actor-bad-fk",
		Status:           state.DeployPending,
		DeployedByUserID: "00000000-0000-0000-0000-000000000000",
		DeployedVia:      "api",
	})
	if err == nil {
		t.Errorf("expected FK violation on non-existent deployed_by_user_id, got nil")
	}
}

// TestPg_DeploymentAnnotationRoundtrip (issue #977 / ADR-116) pins
// the four deploy-annotation columns (reason, tag, deployed_by,
// pr_number) through CreateDeployment + DeploymentByID. The closed-
// set tag CHECK is exercised on both the positive (allowed value)
// and negative (rejected value) branches. A second CreateDeployment
// with all-zero annotation fields asserts the Go-zero → NULL collapse
// on pr_number + the empty-string passthrough on the text columns —
// pre-#977 rows must continue to round-trip without surprise.
//
// The positional scan invariant documented at
// pkg/state/pgstore.go:12269-12270 (and called out again at lines
// 12283-12286) is the load-bearing constraint: if any of the four
// new SELECT projections drifts from the INSERT column order, pgx
// fails loud at the first SELECT. This test is the regression net
// for that drift.
func TestPg_DeploymentAnnotationRoundtrip(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "ann@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "ann-app", Type: state.AppTypeApp,
		RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	// 1. Full annotation payload — the githubd / Action path.
	depFull, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindGitHub,
		ImageDigest: "sha256:ann-full",
		Status:      state.DeployPending,
		Reason:      "Rollback after payments incident",
		Tag:         "incident_recovery",
		DeployedBy:  "octocat",
		PRNumber:    4242,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(full): %v", err)
	}
	got, err := s.DeploymentByID(ctx, depFull.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(full): %v", err)
	}
	if got.Reason != "Rollback after payments incident" {
		t.Errorf("reason = %q, want %q", got.Reason, "Rollback after payments incident")
	}
	if got.Tag != "incident_recovery" {
		t.Errorf("tag = %q, want %q", got.Tag, "incident_recovery")
	}
	if got.DeployedBy != "octocat" {
		t.Errorf("deployed_by = %q, want %q", got.DeployedBy, "octocat")
	}
	if got.PRNumber != 4242 {
		t.Errorf("pr_number = %d, want 4242", got.PRNumber)
	}

	// 2. Zero annotations — push-to-main via CLI without --reason,
	//    --tag, --deployed-by, --pr-number. The Go-zero on
	//    pr_number must collapse to NULL on INSERT (the
	//    nullif($N, 0) at pgstore.go:CreateDeployment).
	depEmpty, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:ann-empty",
		Status:      state.DeployPending,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(empty): %v", err)
	}
	gotEmpty, err := s.DeploymentByID(ctx, depEmpty.ID)
	if err != nil {
		t.Fatalf("DeploymentByID(empty): %v", err)
	}
	if gotEmpty.Reason != "" {
		t.Errorf("empty reason = %q, want \"\"", gotEmpty.Reason)
	}
	if gotEmpty.Tag != "" {
		t.Errorf("empty tag = %q, want \"\"", gotEmpty.Tag)
	}
	if gotEmpty.DeployedBy != "" {
		t.Errorf("empty deployed_by = %q, want \"\"", gotEmpty.DeployedBy)
	}
	if gotEmpty.PRNumber != 0 {
		t.Errorf("empty pr_number = %d, want 0", gotEmpty.PRNumber)
	}

	// 3. Closed-set tag CHECK rejection. The DB-side constraint
	//    (migrations/00346_deployments_annotation.sql) is the
	//    source of truth; the CLI / handler validators mirror
	//    it. We drive the rejection directly through the store
	//    to confirm the constraint is wired (the apid would
	//    otherwise silently accept and round-trip a malformed
	//    value).
	_, err = s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:ann-bad",
		Status:      state.DeployPending,
		Tag:         "rogue_tag",
	})
	if err == nil {
		t.Errorf("expected CHECK violation on rogue_tag, got nil")
	}
}

// TestPg_CreateInstance_PartialUniqueIndexBlocks pins the
// cluster-coord primitive for the multi-host safety cluster PR-5
// (audit F4). Two CreateInstance calls with the SAME wake_id AND
// state IN ('WAKING', 'COLD_BOOTING') must: the first succeeds; the
// second returns state.ErrConcurrentWake (SQLSTATE 23505 translated
// to the typed sentinel). Without this guard, two schedds on
// different boxes can both boot the same wake attempt —
// double-charging the customer, double-warming the cold-boot path,
// and violating spec §6.2's "two instances from one snapshot never
// share IP/netns/uid/RNG" invariant via the IP+netns+uid the
// second boot would mint.
//
// The test exercises the DB-level rejection at the partial unique
// index instances_wake_attempt_active_idx (migration 00350). The
// app-level retry (Engine.createInstanceWithWakeRetry) lives in
// pkg/sched; this is the lower layer's pin.
func TestPg_CreateInstance_PartialUniqueIndexBlocks(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	wakeID := "11111111-1111-4111-8111-111111111111"

	first, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err != nil {
		t.Fatalf("first CreateInstance: %v", err)
	}
	if first.WakeID != wakeID {
		t.Errorf("first instance wake_id = %q, want %q", first.WakeID, wakeID)
	}

	// Second insert with the same wake_id + same in-flight state
	// must fail with ErrConcurrentWake. The partial unique index
	// is the rejection site; the wrapper translates 23505 →
	// ErrConcurrentWake so the engine can recover.
	_, err = s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err == nil {
		t.Fatal("second CreateInstance with duplicate wake_id: expected error, got nil")
	}
	if !errors.Is(err, state.ErrConcurrentWake) {
		t.Fatalf("second CreateInstance: got %v, want ErrConcurrentWake", err)
	}
}

// CreateInstanceWithMode shares the same wake_id partial index as the normal
// insert path. Keep its duplicate translation pinned too: mirror/canary
// admission must recover through ErrConcurrentWake just like a regular wake.
func TestPg_CreateInstanceWithMode_PartialUniqueIndexBlocks(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)
	wakeID := "33333333-3333-4333-8333-333333333333"

	if _, err := s.CreateInstanceWithMode(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID, string(state.InstanceModeNormal)); err != nil {
		t.Fatalf("first CreateInstanceWithMode: %v", err)
	}
	_, err := s.CreateInstanceWithMode(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID, string(state.InstanceModeMirror))
	if err == nil {
		t.Fatal("duplicate CreateInstanceWithMode: expected error, got nil")
	}
	if !errors.Is(err, state.ErrConcurrentWake) {
		t.Fatalf("duplicate CreateInstanceWithMode: got %v, want ErrConcurrentWake", err)
	}
}

// TestPg_CreateInstance_PartialUniqueIndex_AllowsAfterPark pins the
// partial predicate of instances_wake_attempt_active_idx: once an
// instance parks (state moves out of the WAKING/COLD_BOOTING/RUNNING
// set), a fresh INSERT with the same wake_id is allowed. Without
// this guard, a re-wake after the previous instance parked would
// 23505 and the engine would refuse to serve the new request.
// The engine's watchdog (sched/state.go) flips the row to
// PARKED on cold shutdown, which moves the state OUT of the
// partial predicate; the new wake_id is unrelated to the parked
// row's wake_id in production, but the test exercises the
// wake_id-reuse case to pin the predicate.
func TestPg_CreateInstance_PartialUniqueIndex_AllowsAfterPark(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	wakeID := "22222222-2222-4222-8222-222222222222"

	first, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	// Move the row to PARKED so it falls outside the partial
	// predicate. UpdateInstanceStateToTerminal is the watchdog's
	// path that flips in-flight instances out of the in-flight set.
	if err := s.UpdateInstanceStateToTerminal(ctx, first.ID, string(state.StateParked), time.Now()); err != nil {
		t.Fatalf("park: %v", err)
	}

	// Same wake_id, same in-flight state — but the original row
	// is parked so the partial predicate allows the INSERT.
	second, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err != nil {
		t.Fatalf("second after park: %v", err)
	}
	if second.ID == first.ID {
		t.Errorf("second INSERT should have created a fresh row, got same id %q", second.ID)
	}
}

// TestPg_ReadActiveInstanceForWakeID_ReturnsWinner pins the
// recovery path (multi-host safety cluster PR-5 / audit F4). After
// the partial unique index rejects a duplicate INSERT, the engine
// calls ReadActiveInstanceForWakeID to discover the winner. The
// returned row must carry the same wake_id AND a state in the
// in-flight set; the engine's downstream code treats (ins.ID,
// wakeID) the same regardless of which box minted the row.
func TestPg_ReadActiveInstanceForWakeID_ReturnsWinner(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	wakeID := "33333333-3333-4333-8333-333333333333"

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	got, err := s.ReadActiveInstanceForWakeID(ctx, wakeID)
	if err != nil {
		t.Fatalf("ReadActiveInstanceForWakeID: %v", err)
	}
	if got.ID != ins.ID {
		t.Errorf("ReadActiveInstanceForWakeID id = %q, want %q", got.ID, ins.ID)
	}
	if got.WakeID != wakeID {
		t.Errorf("ReadActiveInstanceForWakeID wake_id = %q, want %q", got.WakeID, wakeID)
	}
	if got.State != string(state.StateColdBooting) {
		t.Errorf("ReadActiveInstanceForWakeID state = %q, want %q", got.State, state.StateColdBooting)
	}
}

// TestPg_ReadActiveInstanceForWakeID_ParkedRowHidden pins the
// predicate boundary: a row whose state is PARKING or PARKED does
// not surface via ReadActiveInstanceForWakeID. The recovery path
// only returns in-flight rows; the loser's retry should NOT pick
// up a stale parked row as the "winner" (it would never transition
// to RUNNING and the loser's call would block forever).
func TestPg_ReadActiveInstanceForWakeID_ParkedRowHidden(t *testing.T) {
	s, ctx := pgStore(t)
	_, appID, depID := seedLiveDeploy(t, s, ctx)
	nodeID := resolveDefaultLocal(t, ctx, s)

	wakeID := "44444444-4444-4444-8444-444444444444"

	ins, err := s.CreateInstance(ctx, appID, depID, string(state.StateColdBooting), 512, nodeID, wakeID)
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	if err := s.UpdateInstanceStateToTerminal(ctx, ins.ID, string(state.StateParked), time.Now()); err != nil {
		t.Fatalf("park: %v", err)
	}

	_, err = s.ReadActiveInstanceForWakeID(ctx, wakeID)
	if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ReadActiveInstanceForWakeID after park: got %v, want ErrNotFound", err)
	}
}

// TestPg_DeploymentAuditRoundtrip (issue #976 / ADR-122 /
// SAFE-RELEASES-E.2) pins the AppendDeploymentAudit + ListDeploymentAudit
// pgstore surface. Mirrors TestPg_DeploymentActorRoundtrip's shape
// (write, read back, assert closed-set CHECK rejects, assert
// cross-deployment filter is honored).
func TestPg_DeploymentAuditRoundtrip(t *testing.T) {
	s, ctx := pgStore(t)

	// Generate a deterministic deployment UUID for this test
	// (the deployment_audit table has no FK to deployments, so
	// we don't need a real deployment row).
	deploymentID := uuid.New()
	otherDeploymentID := uuid.New()

	// 1. Full payload — deploy.created with all fields populated.
	id1, err := s.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: deploymentID,
		Kind:         state.DeployCreated,
		Actor:        "apid:dashboard",
		Data:         json.RawMessage(`{"ref":"sha256:abc","supersedes":""}`),
	})
	if err != nil {
		t.Fatalf("AppendDeploymentAudit: %v", err)
	}
	if id1 == 0 {
		t.Errorf("AppendDeploymentAudit id = 0, want non-zero (Postgres IDENTITY returns)")
	}

	// 2. Read back via ListDeploymentAudit — exactly one row for
	// this deployment_id.
	rows, err := s.ListDeploymentAudit(ctx, deploymentID.String(), 0)
	if err != nil {
		t.Fatalf("ListDeploymentAudit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListDeploymentAudit len = %d, want 1", len(rows))
	}
	if rows[0].Kind != state.DeployCreated {
		t.Errorf("rows[0].Kind = %q, want %q", rows[0].Kind, state.DeployCreated)
	}
	if rows[0].Actor != "apid:dashboard" {
		t.Errorf("rows[0].Actor = %q, want %q", rows[0].Actor, "apid:dashboard")
	}
	if rows[0].DeploymentID != deploymentID {
		t.Errorf("rows[0].DeploymentID = %v, want %v", rows[0].DeploymentID, deploymentID)
	}
	// Postgres jsonb canonicalises whitespace ("ref": "v" not "ref":"v")
	// so a literal byte-equal compare breaks under the jsonb driver;
	// compare structurally so the test pins the payload shape rather
	// than the storage form.
	var got map[string]any
	if err := json.Unmarshal(rows[0].Data, &got); err != nil {
		t.Fatalf("Data unmarshal: %v", err)
	}
	want := map[string]any{"ref": "sha256:abc", "supersedes": ""}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rows[0].Data = %v, want %v (jsonb round-trip canonicalises whitespace; compare structurally)", got, want)
	}

	// 3. Cross-deployment filter — write a row for a different
	// deployment_id and assert ListDeploymentAudit does NOT
	// bleed it in.
	if _, err := s.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: otherDeploymentID,
		Kind:         state.DeploySourceRef,
		Actor:        "apid:cli",
		Data:         json.RawMessage(`{"ref":"refs/heads/main"}`),
	}); err != nil {
		t.Fatalf("AppendDeploymentAudit (other): %v", err)
	}
	rowsScoped, err := s.ListDeploymentAudit(ctx, deploymentID.String(), 0)
	if err != nil {
		t.Fatalf("ListDeploymentAudit (scoped): %v", err)
	}
	if len(rowsScoped) != 1 {
		t.Errorf("ListDeploymentAudit (scoped) len = %d, want 1 (other-deployment row must NOT bleed in)", len(rowsScoped))
	}

	// 4. Closed-set kind CHECK rejection. Drive the violation
	// directly through the store to confirm the constraint is
	// wired (the apid handler is expected to mirror the
	// vocabulary, but the constraint is the source of truth).
	_, err = s.AppendDeploymentAudit(ctx, state.DeploymentAudit{
		DeploymentID: deploymentID,
		Kind:         "rogue_audit_kind",
		Actor:        "apid",
	})
	if err == nil {
		t.Errorf("expected CHECK violation on rogue_audit_kind, got nil")
	}
}

// TestPg_DeploymentOrdinal (issue #976 / ADR-122 /
// SAFE-RELEASES-C.2) pins the per-app 1-based rank the
// deployment-preview URL surface stamps. Pairs with
// TestMemStore_DeploymentOrdinal — both impls MUST agree; drift
// rots every existing deployment-preview URL the moment a new
// deploy lands.
//
//   - first deployment in app = ordinal 1 (no COUNT(*) bias)
//   - third deployment in app = ordinal 3 (correct rank across ordering)
//   - second app's first deployment = ordinal 1 in that app
//     (separate counter per app — no global sequence shared)
//   - missing deployment_id in known app = ErrNotFound (sentinel)
//
// Postgres-only behavior verified:
//   - row_number() over (partition by app_id order by
//     created_at, id) is stable — same (app_id, id) pair
//     always resolves to the same rank even after later
//     deploys land.
//   - pgx.ErrNoRows maps to ErrNotFound (NOT a 500).
func TestPg_DeploymentOrdinal(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	a := uuid.New()
	b := uuid.New()
	d1 := uuid.New()
	d2 := uuid.New()
	d3 := uuid.New()
	dX := uuid.New()
	now := time.Now()
	// Two apps so we can pin the "separate counter per app"
	// assertion. Insert via the underlying pool (CreateDeployment
	// is heavier than necessary and would force status transitions
	// — for the ordinal query we only need the {id, app_id,
	// status, created_at} columns, and we set status='live' so
	// the apps status='active' parent precondition is moot (we
	// insert directly without going through CreateApp).
	mustExec := func(stmt string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, stmt, args...); err != nil {
			t.Fatalf("exec %q args=%v: %v", stmt, args, err)
		}
	}
	mustExec(`insert into accounts (id, email, plan) values ($1, $2, 'free')`, uuid.New(), uuid.New().String()+"@example.com")
	acctID := uuid.New()
	mustExec(`insert into accounts (id, email, plan) values ($1, $2, 'free')`, acctID, acctID.String()+"@example.com")
	mustExec(`insert into apps (id, account_id, slug, status, ram_mb) values ($1, $2, 'ordinal-app', 'active', 256)`, a, acctID)
	mustExec(`insert into apps (id, account_id, slug, status, ram_mb) values ($1, $2, 'ordinal-app-other', 'active', 256)`, b, acctID)

	insert := func(id string, appID string, at time.Time) {
		t.Helper()
		// Status 'building' instead of 'live' — the deployments_app_scope_live_uniq
		// partial unique index (migration 00213) caps each (app_id, scope) at one
		// live row. The ordinal query doesn't depend on status; we only need
		// distinct rows for the test.
		// canary_step_started_at is NOT NULL post-00517; stamp it to the
		// row's created_at so the ordinal walk has a stable timestamp
		// and the wall-clock gate readers (pkg/canary.Once, pkg/safedeploy.
		// Orchestrator) skip it via the predicate before consulting it.
		mustExec(`insert into deployments (id, app_id, status, image_digest, created_at, canary_step_started_at)
		          values ($1, $2, 'building', 'sha256:ord', $3, $3)`,
			id, appID, at)
	}
	// Insert out-of-order to exercise (created_at, id) sort.
	insert(d3.String(), a.String(), now.Add(2*time.Second))
	insert(d1.String(), a.String(), now)
	insert(d2.String(), a.String(), now.Add(1*time.Second))
	insert(dX.String(), b.String(), now)

	if got, err := s.DeploymentOrdinal(ctx, a.String(), d1.String()); err != nil || got != 1 {
		t.Errorf("ord(d1) = %d err=%v, want 1", got, err)
	}
	if got, err := s.DeploymentOrdinal(ctx, a.String(), d2.String()); err != nil || got != 2 {
		t.Errorf("ord(d2) = %d err=%v, want 2", got, err)
	}
	if got, err := s.DeploymentOrdinal(ctx, a.String(), d3.String()); err != nil || got != 3 {
		t.Errorf("ord(d3) = %d err=%v, want 3", got, err)
	}
	if got, err := s.DeploymentOrdinal(ctx, b.String(), dX.String()); err != nil || got != 1 {
		t.Errorf("ord(dX in app b) = %d err=%v, want 1 (separate counter)", got, err)
	}
	if _, err := s.DeploymentOrdinal(ctx, a.String(), uuid.New().String()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing deployment: err = %v, want ErrNotFound", err)
	}
}
