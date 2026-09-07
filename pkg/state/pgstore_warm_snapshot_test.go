//go:build !no_pg

// PgStore coverage tests for the snapshot-tier CRUD (issue #470 /
// ADR-055). Mirrors pkg/state/memstore_warm_snapshot_test.go so the
// in-memory and PG paths stay semantically equal — every test name
// has a 1:1 counterpart in the MemStore file.
//
// Build tag matches the rest of the pgstore tests; set
// FAAS_SKIP_PG_TESTS=1 to skip locally (see migrations/README.md).
package state_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedTierAppAndDeploymentPg inserts the minimum app + deployment
// rows the snapshot queries need, plus an account so the foreign-key
// chain is satisfied. UUIDs are fixed so a failure message can name
// them directly; the test doesn't depend on the shape beyond that.
func seedTierAppAndDeploymentPg(t *testing.T, pool *pgxpool.Pool, deploymentID string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		insert into accounts (id, email, plan, created_at)
		values ('00000000-0000-0000-0000-0000000000d0',
		        'tier-pg@example.com', 'pro', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed account: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at)
		values ('00000000-0000-0000-0000-0000000000d1',
		        '00000000-0000-0000-0000-0000000000d0',
		        'tier-pg-app', 'function', 256, 1, 30, 'active', now())
		on conflict (id) do nothing
	`); err != nil {
		t.Fatalf("seed app: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		insert into deployments (id, app_id, image_digest, status, created_at)
		values ($1, '00000000-0000-0000-0000-0000000000d1',
		        'sha256:warm', 'live', now())
		on conflict (id) do nothing
	`, deploymentID); err != nil {
		t.Fatalf("seed deployment: %v", err)
	}
}

// TestPg_CreateSnapshot_DefaultsTierToInit confirms the SQL DEFAULT
// 'init' on snapshots.tier (migration 00110) covers legacy callers
// that don't pass a tier.
func TestPg_CreateSnapshot_DefaultsTierToInit(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v (regression: missing migration slot between 1 and 102)", err)
	}
	depID := "00000000-0000-0000-0000-0000000000e0"
	seedTierAppAndDeploymentPg(t, pool, depID)

	created, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		MemBytes: 1024, DiskBytes: 512, StoredBytes: 384,
		StorageKey: "snap/" + depID + "/mem",
	})
	if err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	if created.Tier != state.SnapshotTierInit {
		t.Errorf("default Tier = %q, want %q", created.Tier, state.SnapshotTierInit)
	}
	if created.StoredBytes != 384 {
		t.Errorf("StoredBytes = %d, want 384", created.StoredBytes)
	}
}

// TestPg_CreateSnapshot_TwoTierCoexist pins the
// (deployment_id, tier) unique-index behaviour from migration 00110:
// init + warm can both exist for one deployment; a duplicate init
// insert hits the unique index and surfaces as ErrConflict (the
// pgstore UniqueViolation branch from R6 — now reachable again).
func TestPg_CreateSnapshot_TwoTierCoexist(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	depID := "00000000-0000-0000-0000-0000000000e1"
	seedTierAppAndDeploymentPg(t, pool, depID)

	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/warm/mem", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}
	_, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/init-dup/mem", Tier: state.SnapshotTierInit,
	})
	if !errors.Is(err, state.ErrConflict) {
		t.Errorf("dup init insert err = %v, want ErrConflict", err)
	}
}

// TestPg_LatestSnapshotForTier confirms the per-tier lookup.
// LatestSnapshotForTier(warm) returns the warm row; (init) returns
// the init row; an unknown deployment returns ErrNotFound.
func TestPg_LatestSnapshotForTier(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	depID := "00000000-0000-0000-0000-0000000000e2"
	seedTierAppAndDeploymentPg(t, pool, depID)

	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/warm/mem", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}

	gotWarm, err := s.LatestSnapshotForTier(ctx, depID, state.SnapshotTierWarm)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier(warm): %v", err)
	}
	if gotWarm.Tier != state.SnapshotTierWarm {
		t.Errorf("warm lookup Tier = %q, want warm", gotWarm.Tier)
	}
	if gotWarm.StorageKey != "snap/"+depID+"/warm/mem" {
		t.Errorf("warm lookup key = %q, want snap/%s/warm/mem", gotWarm.StorageKey, depID)
	}

	gotInit, err := s.LatestSnapshotForTier(ctx, depID, state.SnapshotTierInit)
	if err != nil {
		t.Fatalf("LatestSnapshotForTier(init): %v", err)
	}
	if gotInit.Tier != state.SnapshotTierInit {
		t.Errorf("init lookup Tier = %q, want init", gotInit.Tier)
	}

	if _, err := s.LatestSnapshotForTier(ctx, "00000000-0000-0000-0000-0000000000e9", state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("missing dep warm lookup err = %v, want ErrNotFound", err)
	}
}

// TestPg_LatestSnapshot_WarmBeatsInitOnTie confirms the
// ORDER BY (tier='warm') DESC clause: on a created_at tie, the warm
// row wins.
func TestPg_LatestSnapshot_WarmBeatsInitOnTie(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	depID := "00000000-0000-0000-0000-0000000000e3"
	seedTierAppAndDeploymentPg(t, pool, depID)

	// Insert both rows at the exact same created_at by using a
	// fixed timestamp via pool.Exec — bypasses the per-row clock
	// jitter that would otherwise randomise the tie-breaker.
	if _, err := pool.Exec(ctx, `
		insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, stale, tier, created_at)
		values ($1, 'fc-1.0', 1000, 500, 'snap/`+depID+`/mem', false, 'init', '2026-08-01 00:00:00+00'),
		       ($1, 'fc-1.0', 1000, 500, 'snap/`+depID+`/warm/mem', false, 'warm', '2026-08-01 00:00:00+00')
	`, depID); err != nil {
		t.Fatalf("seed tie rows: %v", err)
	}

	got, err := s.LatestSnapshot(ctx, depID)
	if err != nil {
		t.Fatalf("LatestSnapshot: %v", err)
	}
	if got.Tier != state.SnapshotTierWarm {
		t.Errorf("LatestSnapshot Tier = %q, want warm (tie-break wins)", got.Tier)
	}
}

// TestPg_ListSnapshotsForGC_ProjectsTier confirms the GC projection
// now carries the tier so the perAppKeepCurrentPrevious policy can
// branch on it.
func TestPg_ListSnapshotsForGC_ProjectsTier(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	depID := "00000000-0000-0000-0000-0000000000e4"
	seedTierAppAndDeploymentPg(t, pool, depID)

	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/mem", Tier: state.SnapshotTierInit,
	}); err != nil {
		t.Fatalf("seed init: %v", err)
	}
	if _, err := s.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: depID, FCVersion: "fc-1.0",
		StorageKey: "snap/" + depID + "/warm/mem", Tier: state.SnapshotTierWarm,
	}); err != nil {
		t.Fatalf("seed warm: %v", err)
	}

	got, err := s.ListSnapshotsForGC(ctx)
	if err != nil {
		t.Fatalf("ListSnapshotsForGC: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	tiers := map[string]bool{}
	for _, r := range got {
		if r.ID == "" {
			continue
		}
		tiers[r.Tier] = true
	}
	if !tiers[state.SnapshotTierInit] || !tiers[state.SnapshotTierWarm] {
		t.Errorf("GC projection tiers = %v, want both init + warm", tiers)
	}
}

// seedAppProtocolPg inserts a second app on the same account with
// the given app_protocol (apps_app_protocol_chk enforces {http1,
// http2, grpc}; migration 00382). Returns the new app id.
func seedAppProtocolPg(t *testing.T, pool *pgxpool.Pool, protocol, slug string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx, `
		insert into apps (account_id, slug, type, ram_mb, max_concurrency, idle_timeout_s, status, created_at, app_protocol)
		values ('00000000-0000-0000-0000-0000000000d0',
		        $1, 'function', 256, 1, 30, 'active', now(), $2)
		returning id::text
	`, slug, protocol).Scan(&id); err != nil {
		t.Fatalf("seed %s app: %v", protocol, err)
	}
	return id
}

// TestPg_MarkAllSnapshotsStaleByAppProtocol pins the PG-side mirror
// of memstore_app_protocol_stale_test.go's bulk sweep: every
// non-stale snapshot whose deployment's app.app_protocol ∈
// {http2, grpc} flips stale; http1 is never affected; empty input
// is no-op; second call is idempotent.
//
// Without this test the PG path at pgstore.go:9967 is exercised
// only by imaged's F3 sweep in production; a drift between the
// MemStore and PG semantics (e.g. someone widening
// apps_app_protocol_chk) would silently regress.
func TestPg_MarkAllSnapshotsStaleByAppProtocol(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Seed the tier-pg account row first — seedAppProtocolPg's
	// FK requires it. The depID is unused (we'll create our own
	// deployments below).
	seedTierAppAndDeploymentPg(t, pool, "00000000-0000-0000-0000-0000000000e5")

	// Three apps on the same account, one per protocol. The
	// http1 app's snapshots must NEVER be flipped by an
	// http2+grpc sweep.
	http1App := seedAppProtocolPg(t, pool, "http1", "aprot-pg-h1")
	http2App := seedAppProtocolPg(t, pool, "http2", "aprot-pg-h2")
	grpcApp := seedAppProtocolPg(t, pool, "grpc", "aprot-pg-g")

	// One deployment per app.
	mkDep := func(appID, label string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			insert into deployments (app_id, image_digest, status, created_at)
			values ($1, 'sha256:aprot-' || $2, 'live', now())
			returning id::text
		`, appID, label).Scan(&id); err != nil {
			t.Fatalf("seed dep %s: %v", label, err)
		}
		return id
	}
	http1Dep := mkDep(http1App, "h1")
	http2Dep := mkDep(http2App, "h2")
	grpcDep := mkDep(grpcApp, "g")

	// One snapshot per deployment.
	mkSnap := func(depID, label string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, stale, tier)
			values ($1::uuid, 'fc-1.0', 1000, 500, 'snap/' || $1::text || '/mem', false, 'init')
			returning id::text
		`, depID).Scan(&id); err != nil {
			t.Fatalf("seed snap %s: %v", label, err)
		}
		return id
	}
	http1Snap := mkSnap(http1Dep, "h1")
	http2Snap := mkSnap(http2Dep, "h2")
	grpcSnap := mkSnap(grpcDep, "g")

	// Bulk sweep — F3 close-set is {http2, grpc}.
	n, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http2", "grpc"})
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol: %v", err)
	}
	if n != 2 {
		t.Errorf("marked %d stale, want 2 (http2 + grpc rows)", n)
	}

	// Verify each row directly via SQL — bypasses the read-path
	// projection so a stale stale=true regression surfaces
	// independently of ListSnapshotsForGC.
	staleOf := func(snapID string) bool {
		t.Helper()
		var stale bool
		if err := pool.QueryRow(ctx, `select stale from snapshots where id = $1::uuid`, snapID).Scan(&stale); err != nil {
			t.Fatalf("read stale for %s: %v", snapID, err)
		}
		return stale
	}
	if staleOf(http1Snap) {
		t.Error("http1 snapshot flipped stale — F3 sweep must skip http1 (ADR-126 §Decision 6)")
	}
	if !staleOf(http2Snap) {
		t.Error("http2 snapshot NOT flipped stale — bulk sweep broken")
	}
	if !staleOf(grpcSnap) {
		t.Error("grpc snapshot NOT flipped stale — bulk sweep broken")
	}

	// Idempotency — second call finds zero non-stale rows.
	n2, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, []string{"http2", "grpc"})
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("idempotent second sweep flipped %d, want 0", n2)
	}

	// Empty filter is no-op (matches the SQL behaviour).
	n3, err := s.MarkAllSnapshotsStaleByAppProtocol(ctx, nil)
	if err != nil {
		t.Fatalf("MarkAllSnapshotsStaleByAppProtocol(nil): %v", err)
	}
	if n3 != 0 {
		t.Errorf("empty filter flipped %d, want 0", n3)
	}
}

// TestPg_MarkSnapshotStaleByAppProtocol pins the PG-side mirror of
// memstore_app_protocol_stale_test.go's single-row sweep:
// id+protocol matches → flipped; id+http1 → ErrNotFound (the
// "wrong protocol for this sweep" sentinel); missing id →
// ErrNotFound; empty inputs are caller bugs.
func TestPg_MarkSnapshotStaleByAppProtocol(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Seed the tier-pg account row first — seedAppProtocolPg's
	// FK requires it. The depID is unused (we'll create our own
	// deployments below).
	seedTierAppAndDeploymentPg(t, pool, "00000000-0000-0000-0000-0000000000e6")

	http1App := seedAppProtocolPg(t, pool, "http1", "aprot-single-pg-h1")
	http2App := seedAppProtocolPg(t, pool, "http2", "aprot-single-pg-h2")

	mkSnap := func(appID, label string) string {
		t.Helper()
		var depID string
		if err := pool.QueryRow(ctx, `
			insert into deployments (app_id, image_digest, status, created_at)
			values ($1, 'sha256:aprot-' || $2, 'live', now())
			returning id::text
		`, appID, label).Scan(&depID); err != nil {
			t.Fatalf("seed dep %s: %v", label, err)
		}
		var snapID string
		if err := pool.QueryRow(ctx, `
			insert into snapshots (deployment_id, fc_version, mem_bytes, disk_bytes, storage_key, stale, tier)
			values ($1::uuid, 'fc-1.0', 1000, 500, 'snap/' || $1::text || '/mem', false, 'init')
			returning id::text
		`, depID).Scan(&snapID); err != nil {
			t.Fatalf("seed snap %s: %v", label, err)
		}
		return snapID
	}
	http1Snap := mkSnap(http1App, "h1")
	http2Snap := mkSnap(http2App, "h2")

	// 1. id+protocol matches → flipped, no error.
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, http2Snap, []string{"http2", "grpc"}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	var stale bool
	if err := pool.QueryRow(ctx, `select stale from snapshots where id = $1::uuid`, http2Snap).Scan(&stale); err != nil {
		t.Fatalf("read stale http2: %v", err)
	}
	if !stale {
		t.Error("http2 row not flipped after MarkSnapshotStaleByAppProtocol")
	}

	// 2. id exists + protocol does NOT match (http1 row, sweep = {http2,grpc})
	// → ErrNotFound (caller distinguishes "row exists but wrong protocol" from
	// "row doesn't exist at all").
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, http1Snap, []string{"http2", "grpc"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound for http1 row in http2/grpc sweep, got %v", err)
	}

	// 3. id doesn't exist → ErrNotFound.
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, "00000000-0000-0000-0000-000000000000", []string{"http2"}); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("expected ErrNotFound for missing id, got %v", err)
	}

	// 4. Empty appProtocols is a caller bug.
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, http1Snap, nil); err == nil {
		t.Errorf("expected error for empty appProtocols, got nil")
	}
	if err := s.MarkSnapshotStaleByAppProtocol(ctx, "", []string{"http2"}); err == nil {
		t.Errorf("expected error for empty snapshotID, got nil")
	}
}
