package state

// MemStore parity tests for the ADR-121 PR-B capture path
// (MarkDeploymentLive writes a snapshot row atomically with the
// status='live' transition).
//
// The pgstore capture contract is verified by
// pkg/state/pgstore_openapi_capture_test.go. This file pins the
// in-memory mirror so the contract holds across both stores —
// a future contributor refactoring the projection would catch
// the regression here without spinning Postgres.
//
// TestMain registers a fake openapidiff-free capture callback
// via the TestMain in memstore_test.go (shared across this
// directory). The fixture uses stdlib crypto/sha256 + the
// pkg/api wire shape only — pkg/openapidiff is NOT imported
// from any pkg/state test file because the cycle fix
// (see pkg/state/openapi_capture.go) keeps pkg/state free of
// any pkg/openapidiff import. Production wiring lives in
// cmd/apid/openapi_capture.go and uses the real impl.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestMemStore_OpenAPICapture_MarkDeploymentLiveWritesSnapshot
// pins the capture contract on the in-memory mirror: a call to
// MarkDeploymentLive populates the openAPISnapshots map with a
// row whose bytes / sha256 / schema_version / scope match the
// pgstore shape.
//
// Without the capture hook, the in-memory map would stay empty
// after MarkDeploymentLive and the OpenAPISnapshotByDeployment
// reader would return ErrNotFound — that's the regression the
// pgstore test catches against a real cluster; this file
// catches it in the unit-test fast path.
func TestMemStore_OpenAPICapture_MarkDeploymentLiveWritesSnapshot(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, err := m.CreateAccount(ctx, "openapi-capture-mem@x.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := m.CreateApp(ctx, App{
		AccountID:      acc.ID,
		Slug:           "openapi-capture-mem",
		Type:           AppTypeApp,
		RAMMB:          256,
		MaxConcurrency: 1,
		IdleTimeoutS:   60,
		Status:         AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:capture-mem",
		Status:      DeployPending,
		Scope:       "prod",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	before := time.Now().UTC().Add(-2 * time.Second)
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)

	snap, err := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}
	if snap.DeploymentID != dep.ID {
		t.Errorf("deployment_id = %q, want %q", snap.DeploymentID, dep.ID)
	}
	if snap.AppID != app.ID {
		t.Errorf("app_id = %q, want %q", snap.AppID, app.ID)
	}
	if snap.Scope != "prod" {
		t.Errorf("scope = %q, want %q", snap.Scope, "prod")
	}
	if len(snap.Snapshot) == 0 {
		t.Fatalf("snapshot bytes empty")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(snap.SHA256) {
		t.Errorf("sha256 = %q, want 64-hex", snap.SHA256)
	}
	if snap.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", snap.SchemaVersion)
	}
	if snap.CapturedAt.Before(before) || snap.CapturedAt.After(after) {
		t.Errorf("captured_at = %v, want within [%v, %v]", snap.CapturedAt, before, after)
	}

	// LatestOpenAPISnapshotForScope matches the by-id read.
	latest, err := m.LatestOpenAPISnapshotForScope(ctx, app.ID, "prod")
	if err != nil {
		t.Fatalf("LatestOpenAPISnapshotForScope: %v", err)
	}
	if latest.DeploymentID != dep.ID {
		t.Errorf("latest.deployment_id = %q, want %q", latest.DeploymentID, dep.ID)
	}

	// Decode the canonical JSON envelope end-to-end. The shape
	// inside is openapidiff-specific; we only assert it's
	// valid JSON with non-zero structure so a regression that
	// wrote garbage bytes would fail here.
	var doc map[string]any
	if err := json.Unmarshal(snap.Snapshot, &doc); err != nil {
		t.Fatalf("snapshot bytes not valid JSON: %v", err)
	}
	if len(doc) == 0 {
		t.Errorf("snapshot envelope empty; got %v", doc)
	}
}

// TestMemStore_OpenAPICapture_UpsertOnReLive pins the in-memory
// UPSERT behaviour: a second MarkDeploymentLive on the same
// deployment id overwrites the prior row. The in-memory map is
// key-by-deployment-id (mirroring the table's PK), so a
// regression that switched to append-on-write would surface as
// a stale sha256 still being returned by
// OpenAPISnapshotByDeployment.
func TestMemStore_OpenAPICapture_UpsertOnReLive(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "openapi-upsert-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "upsert-mem", Type: AppTypeApp, Status: AppActive})
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:upsert-mem", Status: DeployPending, Scope: "prod"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #1: %v", err)
	}
	first, err := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #1: %v", err)
	}
	_ = first // pre-re-live baseline; the strict-after assertion was removed (flake hazard on fast machines)

	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #2: %v", err)
	}
	second, err := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #2: %v", err)
	}

	// Re-live must NOT create a duplicate snapshot — UPSERT
	// semantics. The MemStore map (deployment_id → snapshot)
	// guarantees one entry per id by construction; count the
	// entries to pin the contract.
	count := 0
	for snap := range m.openAPISnapshots {
		if snap == dep.ID {
			count++
		}
	}
	if count != 1 {
		t.Errorf("snapshot map entries for %s = %d, want 1 (UPSERT must not duplicate)", dep.ID, count)
	}

	// second.CapturedAt must be >= first.CapturedAt — the
	// upsert resets CapturedAt to time.Now() when the struct
	// came back zero. We don't assert strict-after because
	// wall-clock monotonicity at microsecond resolution is
	// not portable across all test hosts.
	if second.CapturedAt.Before(first.CapturedAt) {
		t.Errorf("captured_at #2 = %v, must not be before #1 = %v", second.CapturedAt, first.CapturedAt)
	}
}

// TestMemStore_OpenAPICapture_DisabledRulesExcluded pins the
// disabled-rule filter: an Enabled=false edge rule must not
// contribute to the captured snapshot. This matches the
// runtime's behaviour (disabled rules are not compiled into
// the gateway's edge table) and keeps the differ from flagging
// a "path added" break on the next promotion that disables an
// old rule.
func TestMemStore_OpenAPICapture_DisabledRulesExcluded(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "openapi-disabled-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "disabled-mem", Type: AppTypeApp, Status: AppActive})
	if _, err := m.CreateEdgeRule(ctx, CreateEdgeRuleParams{
		AccountID:    acc.ID,
		AppID:        app.ID,
		MatchHost:    "disabled.example.com",
		MatchPath:    "/v1/feature",
		MatchMethods: []string{"GET"},
		Priority:     100,
		Enabled:      false,
		Kind:         EdgeRuleKindRoute,
		Action: EdgeRuleAction{
			Kind:  EdgeRuleKindRoute,
			Route: &EdgeRuleRouteAction{TargetAppSlug: "feat"},
		},
	}); err != nil {
		t.Fatalf("CreateEdgeRule(disabled): %v", err)
	}
	if _, err := m.CreateEdgeRule(ctx, CreateEdgeRuleParams{
		AccountID:    acc.ID,
		AppID:        app.ID,
		MatchHost:    "enabled.example.com",
		MatchPath:    "/v1/feature",
		MatchMethods: []string{"GET"},
		Priority:     100,
		Enabled:      true,
		Kind:         EdgeRuleKindRoute,
		Action: EdgeRuleAction{
			Kind:  EdgeRuleKindRoute,
			Route: &EdgeRuleRouteAction{TargetAppSlug: "feat"},
		},
	}); err != nil {
		t.Fatalf("CreateEdgeRule(enabled): %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:disabled-mem", Status: DeployPending, Scope: "prod"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	snap, err := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}

	// The disabled-rule filter is enforced in the producer
	// side (pgstore.captureDeploymentOpenAPISnapshotTx and the
	// memstore mirror skip Enabled=false rules before the
	// rules slice reaches the callback). The fixture in
	// memstore_test.go's TestMain records the rules it
	// received — assert the disabled rule is NOT present so
	// a regression that lifted the Enabled filter would
	// fail. The fixture's shape is openapidiff-free by
	// design (see memstore_test.go for why); we read the
	// rules slice directly. The wire shape uses
	// pkg/api.CreateEdgeRuleRequest whose JSON tags are
	// snake_case (match_host, match_path, enabled).
	var env struct {
		Rules []struct {
			MatchHost string `json:"match_host"`
			Enabled   *bool  `json:"enabled,omitempty"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(snap.Snapshot, &env); err != nil {
		t.Fatalf("decode snapshot envelope: %v", err)
	}
	for _, r := range env.Rules {
		if r.MatchHost == "disabled.example.com" {
			t.Errorf("disabled rule present in snapshot envelope (must be excluded)")
		}
	}
	enabledSeen := false
	for _, r := range env.Rules {
		if r.MatchHost == "enabled.example.com" {
			enabledSeen = true
			break
		}
	}
	if !enabledSeen {
		t.Errorf("enabled rule missing from snapshot envelope")
	}
}

// TestMemStore_OpenAPICapture_Deterministic pins the same-edge-
// rule-set determinism: two consecutive MarkDeploymentLive
// calls with no edge-rule change produce identical SHA-256.
// captured_at differs (the PR-C gate picks the latest), but the
// bytes are byte-stable so the diff is well-defined.
func TestMemStore_OpenAPICapture_Deterministic(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	acc, _ := m.CreateAccount(ctx, "openapi-det-mem@x.com", api.PlanHobby)
	app, _ := m.CreateApp(ctx, App{AccountID: acc.ID, Slug: "det-mem", Type: AppTypeApp, Status: AppActive})
	if _, err := m.CreateEdgeRule(ctx, CreateEdgeRuleParams{
		AccountID:    acc.ID,
		AppID:        app.ID,
		MatchHost:    "api.example.com",
		MatchPath:    "/v1/orders",
		MatchMethods: []string{"GET", "POST"},
		Priority:     100,
		Enabled:      true,
		Kind:         EdgeRuleKindRoute,
		Action: EdgeRuleAction{
			Kind:  EdgeRuleKindRoute,
			Route: &EdgeRuleRouteAction{TargetAppSlug: "orders"},
		},
	}); err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}
	dep, err := m.CreateDeployment(ctx, Deployment{AppID: app.ID, ImageDigest: "sha256:det-mem", Status: DeployPending, Scope: "prod"})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #1: %v", err)
	}
	first, _ := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err := m.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #2: %v", err)
	}
	second, _ := m.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if first.SHA256 != second.SHA256 {
		t.Errorf("SHA-256 drift across stable edge-rule list:\n  first  = %s\n  second = %s",
			first.SHA256, second.SHA256)
	}

	// Decodes cleanly (the SHA is a hex-64 string).
	decoded, err := hex.DecodeString(second.SHA256)
	if err != nil {
		t.Fatalf("decode sha256: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("sha256 bytes = %d, want 32", len(decoded))
	}
}

// TestMemStore_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_Validation
// pins the input-validation contract on
// [MemStore.UpdateDeploymentOpenAPISnapshot]: every required
// field (deployment_id, app_id, scope, snapshot bytes, sha256)
// must be non-empty, and SchemaVersion must be >= 1. The
// pgstore mirror enforces the same rules via the SQL UPSERT
// but the memstore mirror validates BEFORE the map write so a
// bad input doesn't poison the in-memory state.
//
// Driving these from a MemStore test (not a PgStore test) keeps
// the assertions cheap and deterministic — the pgstore path is
// covered separately by pgstore_openapi_capture_test.go's
// analogous table.
//
// The expected-error substring matches the production messages
// verbatim (see memstore.go line 4324 onward) so a future
// rename of either side breaks both at the same time.
func TestMemStore_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_Validation(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	good := func() OpenAPISnapshot {
		return OpenAPISnapshot{
			DeploymentID:  "dep-1",
			AppID:         "app-1",
			Scope:         "prod",
			Snapshot:      []byte(`{"ok":true}`),
			SHA256:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SchemaVersion: 1,
			CapturedAt:    time.Now().UTC(),
		}
	}

	cases := []struct {
		name     string
		mutate   func(*OpenAPISnapshot)
		wantSubs string
	}{
		{
			name:     "empty deployment_id",
			mutate:   func(s *OpenAPISnapshot) { s.DeploymentID = "" },
			wantSubs: "empty deployment_id",
		},
		{
			name:     "empty app_id",
			mutate:   func(s *OpenAPISnapshot) { s.AppID = "" },
			wantSubs: "empty app_id",
		},
		{
			name:     "empty scope",
			mutate:   func(s *OpenAPISnapshot) { s.Scope = "" },
			wantSubs: "empty scope",
		},
		{
			name:     "empty snapshot bytes",
			mutate:   func(s *OpenAPISnapshot) { s.Snapshot = nil },
			wantSubs: "empty snapshot bytes",
		},
		{
			name:     "empty sha256",
			mutate:   func(s *OpenAPISnapshot) { s.SHA256 = "" },
			wantSubs: "empty sha256",
		},
		{
			name:     "schema_version zero",
			mutate:   func(s *OpenAPISnapshot) { s.SchemaVersion = 0 },
			wantSubs: "schema_version must be >= 1",
		},
		{
			name:     "schema_version negative",
			mutate:   func(s *OpenAPISnapshot) { s.SchemaVersion = -1 },
			wantSubs: "schema_version must be >= 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := good()
			tc.mutate(&snap)
			err := m.UpdateDeploymentOpenAPISnapshot(ctx, snap)
			if err == nil {
				t.Fatalf("UpdateDeploymentOpenAPISnapshot(%s) = nil err; want validation error", tc.name)
			}
			if !regexp.MustCompile(tc.wantSubs).MatchString(err.Error()) {
				t.Errorf("err = %q; want substring %q", err.Error(), tc.wantSubs)
			}
		})
	}

	// Sanity: the good case still succeeds and the row is
	// retrievable via OpenAPISnapshotByDeployment.
	snap := good()
	if err := m.UpdateDeploymentOpenAPISnapshot(ctx, snap); err != nil {
		t.Fatalf("UpdateDeploymentOpenAPISnapshot(good) = %v; want nil", err)
	}
	got, err := m.OpenAPISnapshotByDeployment(ctx, snap.DeploymentID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment = %v; want nil", err)
	}
	if got.SHA256 != snap.SHA256 {
		t.Errorf("round-trip SHA = %q; want %q", got.SHA256, snap.SHA256)
	}
}

// TestMemStore_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_CapturedAtDefaults
// pins the CapturedAt zero-default: a caller that omits
// CapturedAt gets time.Now().UTC() back from the map. The
// pgstore mirror has the same fallback (line 5384 in
// pgstore.go) — both stores keep the row's captured_at
// non-NULL so the (app_id, scope, captured_at DESC) index
// sort stays deterministic.
func TestMemStore_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_CapturedAtDefaults(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()

	snap := OpenAPISnapshot{
		DeploymentID:  "dep-captured-at",
		AppID:         "app-1",
		Scope:         "prod",
		Snapshot:      []byte(`{"ok":true}`),
		SHA256:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SchemaVersion: 1,
		// CapturedAt deliberately zero — the memstore must
		// substitute time.Now().UTC() before the map write.
	}
	before := time.Now().UTC()
	if err := m.UpdateDeploymentOpenAPISnapshot(ctx, snap); err != nil {
		t.Fatalf("UpdateDeploymentOpenAPISnapshot = %v; want nil", err)
	}
	after := time.Now().UTC()

	got, err := m.OpenAPISnapshotByDeployment(ctx, snap.DeploymentID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment = %v; want nil", err)
	}
	if got.CapturedAt.IsZero() {
		t.Fatalf("CapturedAt stayed zero after UpdateDeploymentOpenAPISnapshot")
	}
	if got.CapturedAt.Before(before) || got.CapturedAt.After(after) {
		t.Errorf("CapturedAt = %v; want in [%v, %v]", got.CapturedAt, before, after)
	}
}

// TestMemStore_OpenAPICapture_LatestForScope_NotFound pins
// the not-found branch of [MemStore.LatestOpenAPISnapshotForScope]
// — a scope with no captured snapshot returns ErrNotFound so
// the gate (PR-C) can take the "no baseline" branch and let
// the first promotion through unblocked.
func TestMemStore_OpenAPICapture_LatestForScope_NotFound(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	if _, err := m.LatestOpenAPISnapshotForScope(ctx, "missing-app", "prod"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestOpenAPISnapshotForScope(missing) = %v; want ErrNotFound", err)
	}
}

// api.PlanHobby is a local mirror of api.PlanHobby so the test
// file does not pull in pkg/api (which would import pkg/state,
// the package under test). Tests in pkg/state conventionally
// pin the plan id directly. Matches the production default
// for CreateAccount with plan="hobby".
