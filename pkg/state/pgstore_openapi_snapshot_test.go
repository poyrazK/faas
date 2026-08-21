package state_test

// PgStore parity tests for the new OpenAPISnapshot Store methods
// (ADR-121, migration 00358). The migration-pinned schema is
// verified by migrations/00358_deployment_openapi_snapshots_test.go;
// this file pins the *hand-written SQL* in pgstore.go
// (UpdateDeploymentOpenAPISnapshot, LatestOpenAPISnapshotForScope,
// OpenAPISnapshotByDeployment) against a real cluster.
//
// MemStore parity lives in pkg/state/memstore_test.go (the
// TestMemStore_OpenAPISnapshot_* cases). Skips on FAAS_SKIP_PG_TESTS
// and on no Postgres.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedOpenAPISnapshotApp creates an account + app directly and
// returns their ids. Tests using this helper then write a minimal
// `deployments` row themselves so they can fully control the
// scope + lifecycle status.
func seedOpenAPISnapshotApp(t *testing.T, s *state.PgStore, ctx context.Context, prefix string) (appID string) {
	t.Helper()
	email := fmt.Sprintf("%s-%d@example.com", prefix, time.Now().UnixNano())
	acct, err := s.CreateAccount(ctx, email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	limits := api.MustLimitsFor(acct.Plan)
	app, err := s.CreateAppIfUnderQuota(ctx, state.App{
		AccountID:      acct.ID,
		Slug:           prefix + "-" + acct.ID,
		Type:           state.AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
		Status:         state.AppActive,
	}, limits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	return app.ID
}

// TestPg_OpenAPISnapshot_RoundTrip pins the production SQL path:
// UpdateDeploymentOpenAPISnapshot writes a row, the by-id and
// by-scope reads return it identically.
func TestPg_OpenAPISnapshot_RoundTrip(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-snapshot-rt")
	var depID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO deployments (id, app_id, image_digest, status, scope)
		VALUES (gen_random_uuid(), $1, 'sha256:test-openapi-snapshot-round-trip', 'live', 'prod')
		RETURNING id
	`, appID).Scan(&depID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	snap := state.OpenAPISnapshot{
		DeploymentID:  depID,
		AppID:         appID,
		Scope:         "prod",
		Snapshot:      json.RawMessage(`{"schema_version":1,"spec":{}}`),
		SHA256:        "0000000000000000000000000000000000000000000000000000000000000000",
		SchemaVersion: 1,
		CapturedAt:    now,
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, snap); err != nil {
		t.Fatalf("UpdateDeploymentOpenAPISnapshot: %v", err)
	}

	// By id.
	got, err := s.OpenAPISnapshotByDeployment(ctx, depID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}
	if got.DeploymentID != snap.DeploymentID || got.AppID != snap.AppID || got.Scope != snap.Scope {
		t.Errorf("round-trip mismatch: got %+v", got)
	}
	// Postgres jsonb normalises key order on storage, so we
	// compare structurally via canonical unmarshal rather than
	// raw bytes.
	var wantShape, gotShape map[string]any
	_ = json.Unmarshal(snap.Snapshot, &wantShape)
	_ = json.Unmarshal(got.Snapshot, &gotShape)
	if !reflect.DeepEqual(gotShape, wantShape) {
		t.Errorf("Snapshot jsonb round-trip structural mismatch: got %s want %s", got.Snapshot, snap.Snapshot)
	}
	if got.SHA256 != snap.SHA256 || got.SchemaVersion != 1 {
		t.Errorf("metadata round-trip: got %+v", got)
	}
	if !got.CapturedAt.Equal(now) {
		t.Errorf("CapturedAt: got %v want %v", got.CapturedAt, now)
	}

	// By scope.
	got2, err := s.LatestOpenAPISnapshotForScope(ctx, appID, "prod")
	if err != nil {
		t.Fatalf("LatestOpenAPISnapshotForScope: %v", err)
	}
	if got2.DeploymentID != depID {
		t.Errorf("LatestOpenAPISnapshotForScope: got %q", got2.DeploymentID)
	}
}

// TestPg_OpenAPISnapshot_Upsert pins the UPSERT semantics: a
// second write for the same deployment_id must overwrite the
// previous row. The deployment_id PK + ON CONFLICT DO UPDATE
// clause together guarantee a single row per deployment.
func TestPg_OpenAPISnapshot_Upsert(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-snapshot-upsert")
	var depID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO deployments (id, app_id, image_digest, status, scope) VALUES (gen_random_uuid(), $1, 'sha256:test-openapi-snapshot-upsert', 'live', 'prod') RETURNING id
	`, appID).Scan(&depID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}

	first := state.OpenAPISnapshot{
		DeploymentID: depID, AppID: appID, Scope: "prod",
		Snapshot:      json.RawMessage(`{"schema_version":1,"first":true}`),
		SHA256:        "1111111111111111111111111111111111111111111111111111111111111111",
		SchemaVersion: 1,
		CapturedAt:    time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
	}
	second := state.OpenAPISnapshot{
		DeploymentID: depID, AppID: appID, Scope: "prod",
		Snapshot:      json.RawMessage(`{"schema_version":1,"second":true}`),
		SHA256:        "2222222222222222222222222222222222222222222222222222222222222222",
		SchemaVersion: 1,
		CapturedAt:    time.Now().UTC().Truncate(time.Second),
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, first); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, second); err != nil {
		t.Fatalf("second: %v", err)
	}
	got, err := s.OpenAPISnapshotByDeployment(ctx, depID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}
	if got.SHA256 != second.SHA256 {
		t.Errorf("UPSERT must overwrite sha256; got %q want %q", got.SHA256, second.SHA256)
	}
	// Postgres jsonb normalises key order, so compare structurally.
	var wantShape, gotShape map[string]any
	_ = json.Unmarshal(second.Snapshot, &wantShape)
	_ = json.Unmarshal(got.Snapshot, &gotShape)
	if !reflect.DeepEqual(gotShape, wantShape) {
		t.Errorf("UPSERT must overwrite snapshot: got %s want %s", got.Snapshot, second.Snapshot)
	}

	// Count check: exactly one row for the deployment.
	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployment_openapi_snapshots WHERE deployment_id = $1::uuid`, depID).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("expected exactly 1 row; got %d", count)
	}
}

// TestPg_OpenAPISnapshot_NotFound pins the ErrNotFound contract
// for both read paths. The PR-C gate treats "no baseline" as
// "no break possible" so the first promotion after the flag
// is flipped is ungated. The deployment_id / app_id columns
// are uuid (migration 00358), so the "missing" lookups use a
// well-formed uuid that has no row in the table — a malformed
// value trips SQLSTATE 22P02 (invalid_text_representation)
// which is a different gate entirely.
func TestPg_OpenAPISnapshot_NotFound(t *testing.T) {
	s, ctx := pgStore(t)
	missingDeployment := "00000000-0000-0000-0000-000000000099"
	missingApp := "00000000-0000-0000-0000-000000000098"
	if _, err := s.OpenAPISnapshotByDeployment(ctx, missingDeployment); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("OpenAPISnapshotByDeployment missing: expected ErrNotFound, got %v", err)
	}
	if _, err := s.LatestOpenAPISnapshotForScope(ctx, missingApp, "prod"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("LatestOpenAPISnapshotForScope missing: expected ErrNotFound, got %v", err)
	}
}

// TestPg_OpenAPISnapshot_LatestByScope pins that the read
// picks the most recent captured_at across multiple rows for
// the same (app_id, scope). The (app_id, scope, captured_at
// DESC) index added in migration 00358 backs the lookup.
func TestPg_OpenAPISnapshot_LatestByScope(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-snapshot-latest")
	var olderID, newerID string
	if err := pool.QueryRow(ctx, `INSERT INTO deployments (id, app_id, image_digest, status, scope) VALUES (gen_random_uuid(), $1, 'sha256:test-openapi-snapshot-older', 'superseded', 'prod') RETURNING id`, appID).Scan(&olderID); err != nil {
		t.Fatalf("insert older dep: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO deployments (id, app_id, image_digest, status, scope) VALUES (gen_random_uuid(), $1, 'sha256:test-openapi-snapshot-newer', 'live', 'prod') RETURNING id`, appID).Scan(&newerID); err != nil {
		t.Fatalf("insert newer dep: %v", err)
	}
	older := state.OpenAPISnapshot{
		DeploymentID: olderID, AppID: appID, Scope: "prod",
		Snapshot:      json.RawMessage(`{}`),
		SHA256:        "1111111111111111111111111111111111111111111111111111111111111111",
		SchemaVersion: 1,
		CapturedAt:    time.Now().UTC().Add(-time.Hour).Truncate(time.Second),
	}
	newer := state.OpenAPISnapshot{
		DeploymentID: newerID, AppID: appID, Scope: "prod",
		Snapshot:      json.RawMessage(`{}`),
		SHA256:        "2222222222222222222222222222222222222222222222222222222222222222",
		SchemaVersion: 1,
		CapturedAt:    time.Now().UTC().Truncate(time.Second),
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, older); err != nil {
		t.Fatalf("older: %v", err)
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, newer); err != nil {
		t.Fatalf("newer: %v", err)
	}
	got, err := s.LatestOpenAPISnapshotForScope(ctx, appID, "prod")
	if err != nil {
		t.Fatalf("LatestOpenAPISnapshotForScope: %v", err)
	}
	if got.DeploymentID != newerID {
		t.Errorf("LatestOpenAPISnapshotForScope must pick newer; got %q", got.DeploymentID)
	}
}

// TestPg_OpenAPISnapshot_CascadeOnDeploymentDelete pins the
// load-bearing ON DELETE CASCADE from deployment_openapi_snapshots
// to deployments. A manual deployment purge (e.g. operator SQL
// cleanup) must sweep the snapshot row — otherwise the table
// accumulates orphans that lead to ghostly "current baseline"
// hits in the gate.
func TestPg_OpenAPISnapshot_CascadeOnDeploymentDelete(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-snapshot-cascade")
	var depID string
	if err := pool.QueryRow(ctx, `INSERT INTO deployments (id, app_id, image_digest, status, scope) VALUES (gen_random_uuid(), $1, 'sha256:test-openapi-snapshot-cascade', 'live', 'prod') RETURNING id`, appID).Scan(&depID); err != nil {
		t.Fatalf("insert deployment: %v", err)
	}
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, state.OpenAPISnapshot{
		DeploymentID: depID, AppID: appID, Scope: "prod",
		Snapshot: json.RawMessage(`{}`), SHA256: "5555555555555555555555555555555555555555555555555555555555555555",
		SchemaVersion: 1, CapturedAt: time.Now().UTC().Truncate(time.Second),
	}); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Sanity: row exists.
	var n int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployment_openapi_snapshots WHERE deployment_id = $1::uuid`, depID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("snapshot row missing before cascade: %d", n)
	}
	// Trigger the cascade.
	if _, err := pool.Exec(ctx, `DELETE FROM deployments WHERE id = $1`, depID); err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM deployment_openapi_snapshots WHERE deployment_id = $1::uuid`, depID).Scan(&n); err != nil {
		t.Fatalf("post-cascade count: %v", err)
	}
	if n != 0 {
		t.Errorf("ON DELETE CASCADE must purge snapshot; got %d rows", n)
	}
}
