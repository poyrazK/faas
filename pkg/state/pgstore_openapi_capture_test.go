package state_test

// PgStore parity tests for the ADR-121 PR-B capture path. The
// migration-pinned schema is verified by
// migrations/00358_deployment_openapi_snapshots_test.go; this
// file pins the *capture writer* in pgstore.go
// (MarkDeploymentLive's in-tx UPSERT into
// deployment_openapi_snapshots) against a real cluster.
//
// MemStore parity lives in
// pkg/state/memstore_openapi_capture_test.go. Skips on
// FAAS_SKIP_PG_TESTS and on no Postgres.

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPg_OpenAPICapture_MarkDeploymentLiveWritesSnapshot pins the
// PR-B capture contract: a call to MarkDeploymentLive writes a
// deployment_openapi_snapshots row atomically with the
// status='live' UPDATE. The snapshot row must carry:
//
//   - the deployment id (PK match),
//   - the app id (FK match),
//   - the scope from the deployments row,
//   - non-empty canonical-JSON bytes,
//   - a 64-hex-char sha256,
//   - schema_version=1 (the SnapshotSchemaVersion constant),
//   - a captured_at timestamp within a sane window of the call.
//
// The diff is the live transition is the entry point — a
// regression that silently drops the UPSERT would surface here
// as a missing row, which PR-C's gate would experience as "no
// baseline, never block".
func TestPg_OpenAPICapture_MarkDeploymentLiveWritesSnapshot(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-capture-write")

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:capture-write",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	before := time.Now().UTC().Add(-2 * time.Second)
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	after := time.Now().UTC().Add(2 * time.Second)

	got, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}
	if got.DeploymentID != dep.ID {
		t.Errorf("deployment_id = %q, want %q", got.DeploymentID, dep.ID)
	}
	if got.AppID != appID {
		t.Errorf("app_id = %q, want %q", got.AppID, appID)
	}
	if got.Scope != "prod" {
		t.Errorf("scope = %q, want %q", got.Scope, "prod")
	}
	if len(got.Snapshot) == 0 {
		t.Fatalf("snapshot bytes empty")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got.SHA256) {
		t.Errorf("sha256 = %q, want 64-hex", got.SHA256)
	}
	if got.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", got.SchemaVersion)
	}
	if got.CapturedAt.Before(before) || got.CapturedAt.After(after) {
		t.Errorf("captured_at = %v, want within [%v, %v]", got.CapturedAt, before, after)
	}

	// Round-trip decode so we pin the snapshot shape end-to-end:
	// capture writer wrote canonical JSON, future reader decodes
	// it without error and re-marshals to the same SHA-256.
	var doc map[string]any
	if err := json.Unmarshal(got.Snapshot, &doc); err != nil {
		t.Fatalf("snapshot bytes not valid JSON: %v", err)
	}
	if _, ok := doc["spec"]; !ok {
		t.Errorf("snapshot missing spec field; got %v", doc)
	}
	if v, ok := doc["schema_version"].(float64); !ok || int(v) != 1 {
		t.Errorf("snapshot schema_version = %v, want 1", doc["schema_version"])
	}
}

// TestPg_OpenAPICapture_MarkDeploymentLiveUpsertsOnReLive pins
// the UPSERT semantics on a re-promotion: the second
// MarkDeploymentLive on the same deployment id (the redelivery
// path) overwrites the prior snapshot row in place. Without the
// UPSERT, a redelivery would land a second row and trip
// SQLSTATE 23505 (PK violation).
//
// SHA-256 is a fresh value — the second live transition stamps a
// fresh captured_at, even if the edge-rule list is unchanged.
// (Determinism is per-snapshot, not across snapshots; the
// previous row's hash is irrelevant once we overwrite.)
func TestPg_OpenAPICapture_MarkDeploymentLiveUpsertsOnReLive(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-capture-upsert")

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:capture-upsert",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #1: %v", err)
	}
	first, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #1: %v", err)
	}
	_ = first // pre-re-live baseline; the strict-after assertion was removed (see comment below)

	// Count snapshots for this deployment BEFORE the second
	// transition: must be 1 (UPSERT, not INSERT, so a re-live
	// does not create a duplicate row). Asserting this here
	// pins the on-conflict-do-update SQL contract.
	var preCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from deployment_openapi_snapshots where deployment_id = $1::uuid`,
		dep.ID,
	).Scan(&preCount); err != nil {
		t.Fatalf("count snapshots pre: %v", err)
	}
	if preCount != 1 {
		t.Fatalf("snapshot row count before re-live = %d, want 1", preCount)
	}

	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #2: %v", err)
	}
	second, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #2: %v", err)
	}

	// Re-live must NOT create a duplicate row — the UPSERT
	// overwrites. Wall-clock captured_at moves forward (or
	// stays equal at microsecond resolution on fast machines);
	// we do NOT assert strict-after to avoid the time.Sleep
	// flake that motivated this rewrite.
	var postCount int
	if err := pool.QueryRow(ctx,
		`select count(*) from deployment_openapi_snapshots where deployment_id = $1::uuid`,
		dep.ID,
	).Scan(&postCount); err != nil {
		t.Fatalf("count snapshots post: %v", err)
	}
	if postCount != 1 {
		t.Errorf("snapshot row count after re-live = %d, want 1 (UPSERT must not duplicate)", postCount)
	}

	// LatestOpenAPISnapshotForScope returns the freshest row.
	latest, err := s.LatestOpenAPISnapshotForScope(ctx, appID, "prod")
	if err != nil {
		t.Fatalf("LatestOpenAPISnapshotForScope: %v", err)
	}
	if latest.DeploymentID != dep.ID || !latest.CapturedAt.Equal(second.CapturedAt) {
		t.Errorf("LatestOpenAPISnapshotForScope = %+v, want %+v", latest, second)
	}
}

// TestPg_OpenAPICapture_DisabledRulesExcluded pins the disabled-
// rule filter: an edge rule with Enabled=false must NOT
// contribute a route path to the captured snapshot. The runtime
// never compiles a disabled rule into the gateway's edge table;
// the snapshot must mirror what the customer can actually
// serve, or PR-C's differ would flag a "path added" break on
// the next promotion that disables an old rule.
func TestPg_OpenAPICapture_DisabledRulesExcluded(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	acctID, appID := seedOpenAPICaptureAppWithAcct(t, s, ctx, "openapi-capture-disabled")

	// Build the OpenAPI projection manually the same way the
	// capture writer does, so we can assert the disabled rule is
	// not in the spec without a backdoor into the writer.
	disabledHost := "disabled.example.com"
	enabledHost := "enabled.example.com"
	for _, c := range []struct {
		host    string
		enabled bool
	}{
		{disabledHost, false},
		{enabledHost, true},
	} {
		if _, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
			AccountID:    acctID,
			AppID:        appID,
			MatchHost:    c.host,
			MatchPath:    "/v1/feature",
			MatchMethods: []string{"GET"},
			Priority:     100,
			Enabled:      c.enabled,
			Kind:         state.EdgeRuleKindRoute,
			Action: state.EdgeRuleAction{
				Kind: state.EdgeRuleKindRoute,
				Route: &state.EdgeRuleRouteAction{
					TargetAppSlug: "feat",
				},
			},
		}); err != nil {
			t.Fatalf("CreateEdgeRule(%s, enabled=%v): %v", c.host, c.enabled, err)
		}
	}

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:capture-disabled",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive: %v", err)
	}
	snap, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment: %v", err)
	}

	// Decode and walk the captured spec — the snapshot is the
	// canonical envelope {"schema_version": 1, "spec": {...}}.
	var doc struct {
		SchemaVersion int `json:"schema_version"`
		Spec          struct {
			Paths map[string]any `json:"paths"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(snap.Snapshot, &doc); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}

	enabledKey := enabledHost + "/v1/feature"
	disabledKey := disabledHost + "/v1/feature"
	if _, ok := doc.Spec.Paths[enabledKey]; !ok {
		t.Errorf("enabled rule path %q missing from snapshot (paths = %v)",
			enabledKey, keysOf(doc.Spec.Paths))
	}
	if _, ok := doc.Spec.Paths[disabledKey]; ok {
		t.Errorf("disabled rule path %q present in snapshot (must be excluded)", disabledKey)
	}

	// SHA-256 sanity: hex-64 shape, decodes cleanly.
	decoded, err := hex.DecodeString(snap.SHA256)
	if err != nil {
		t.Fatalf("decode sha256: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("sha256 bytes = %d, want 32", len(decoded))
	}
}

// TestPg_OpenAPICapture_DeterministicForSameEdgeRules pins
// determinism: two consecutive MarkDeploymentLive calls on a
// deployment whose edge-rule list hasn't changed produce the
// same SHA-256 (captured_at differs, but the bytes don't).
// The SHA-256 is the replay anchor for the snapshot row — a
// drift would imply the differ saw the spec change, which is
// exactly the regression we want to catch.
func TestPg_OpenAPICapture_DeterministicForSameEdgeRules(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	acctID, appID := seedOpenAPICaptureAppWithAcct(t, s, ctx, "openapi-capture-determinism")

	if _, err := s.CreateEdgeRule(ctx, state.CreateEdgeRuleParams{
		AccountID:    acctID,
		AppID:        appID,
		MatchHost:    "api.example.com",
		MatchPath:    "/v1/orders",
		MatchMethods: []string{"GET", "POST"},
		Priority:     100,
		Enabled:      true,
		Kind:         state.EdgeRuleKindRoute,
		Action: state.EdgeRuleAction{
			Kind: state.EdgeRuleKindRoute,
			Route: &state.EdgeRuleRouteAction{
				TargetAppSlug: "orders",
			},
		},
	}); err != nil {
		t.Fatalf("CreateEdgeRule: %v", err)
	}

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:capture-det",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #1: %v", err)
	}
	first, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #1: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatalf("MarkDeploymentLive #2: %v", err)
	}
	second, err := s.OpenAPISnapshotByDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment #2: %v", err)
	}
	if first.SHA256 != second.SHA256 {
		t.Errorf("SHA-256 drift across stable edge-rule list:\n  first  = %s\n  second = %s",
			first.SHA256, second.SHA256)
	}
	if first.SchemaVersion != second.SchemaVersion {
		t.Errorf("schema_version drift: %d → %d", first.SchemaVersion, second.SchemaVersion)
	}
}

// TestPg_OpenAPICapture_RollbackPathWritesSnapshot pins the
// secondary capture site: the apid rollback handler
// (cmd/apid/handlers_ext.go's POST /v1/apps/{slug}/rollback)
// also flips a deployment to 'live' via MarkDeploymentLive.
// The PR-B capture must fire on that path too — a rollback
// target promoted without a snapshot would leave the prod-
// promotion gate with a stale baseline.
//
// We exercise the path by simulating the rollback sequence
// directly: MarkDeploymentSuperseded on the prior + MarkDeploymentLive
// on the target.
func TestPg_OpenAPICapture_RollbackPathWritesSnapshot(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	appID := seedOpenAPISnapshotApp(t, s, ctx, "openapi-capture-rollback")

	// Prior live deployment (target of the supersede).
	depPrior, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:rollback-prior",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(prior): %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depPrior.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(prior): %v", err)
	}
	priorSnap, err := s.OpenAPISnapshotByDeployment(ctx, depPrior.ID)
	if err != nil {
		t.Fatalf("prior snapshot: %v", err)
	}

	// Rollback target (the customer's previously-superseded row).
	depTarget, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       appID,
		ImageDigest: "sha256:rollback-target",
		Status:      state.DeployPending,
		Scope:       "prod",
		Kind:        state.DeploymentKindImage,
	})
	if err != nil {
		t.Fatalf("CreateDeployment(target): %v", err)
	}

	// Rollback sequence: supersede prior, promote target.
	if err := s.MarkDeploymentSuperseded(ctx, depPrior.ID); err != nil {
		t.Fatalf("MarkDeploymentSuperseded: %v", err)
	}
	if err := s.MarkDeploymentLive(ctx, depTarget.ID); err != nil {
		t.Fatalf("MarkDeploymentLive(target): %v", err)
	}

	targetSnap, err := s.OpenAPISnapshotByDeployment(ctx, depTarget.ID)
	if err != nil {
		t.Fatalf("target snapshot: %v", err)
	}
	if targetSnap.Scope != "prod" || targetSnap.AppID != appID {
		t.Errorf("target snapshot wrong: %+v", targetSnap)
	}

	// LatestOpenAPISnapshotForScope now points at the target
	// (the rollback promotion).
	latest, err := s.LatestOpenAPISnapshotForScope(ctx, appID, "prod")
	if err != nil {
		t.Fatalf("LatestOpenAPISnapshotForScope: %v", err)
	}
	if latest.DeploymentID != depTarget.ID {
		t.Errorf("latest = %s, want target %s", latest.DeploymentID, depTarget.ID)
	}

	// And the prior row is still there for history.
	if _, err := s.OpenAPISnapshotByDeployment(ctx, depPrior.ID); err != nil {
		t.Errorf("prior snapshot vanished after rollback: %v", err)
	}
	if priorSnap.SHA256 == targetSnap.SHA256 && depPrior.ImageDigest != depTarget.ImageDigest {
		// This is informational — the SHA could legitimately
		// match if the projection is the same (no edge rules),
		// but the captured_at differs.
		t.Logf("prior and target snapshot hashes match (no edge rules → projection is identical): %s", priorSnap.SHA256)
	}
}

// seedOpenAPICaptureAppWithAcct is a sibling of
// seedOpenAPISnapshotApp that also returns the account id so
// tests can author edge rules (CreateEdgeRuleParams requires
// both). Mirrors the same shape — direct CreateAccount +
// CreateAppIfUnderQuota — so the fixture is independent of any
// customer-facing handler.
func seedOpenAPICaptureAppWithAcct(t *testing.T, s *state.PgStore, ctx context.Context, prefix string) (acctID, appID string) {
	t.Helper()
	email := prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 10) + "@example.com"
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
	return acct.ID, app.ID
}

// keysOf is a small helper for failure messages — print the
// keys of a map[string]any so a missing-path test failure
// shows what the snapshot actually contains.
func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPg_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_Validation
// pins the input-validation contract on
// [PgStore.UpdateDeploymentOpenAPISnapshot]: every required
// field (deployment_id, app_id, scope, snapshot bytes, sha256)
// must be non-empty, and SchemaVersion must be >= 1. The
// MemStore mirror has the same checks (pinned by
// memstore_openapi_capture_test.go's validation test); this
// file pins the pgstore mirror against a real cluster.
//
// Each case asserts the substring the production error
// message emits (pgstore.go line 5366 onward) so a future
// refactor that drops or rewrites the validation breaks both
// stores at the same time.
func TestPg_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_Validation(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	good := func() state.OpenAPISnapshot {
		return state.OpenAPISnapshot{
			DeploymentID:  "11111111-1111-1111-1111-111111111111",
			AppID:         "22222222-2222-2222-2222-222222222222",
			Scope:         "prod",
			Snapshot:      []byte(`{"ok":true}`),
			SHA256:        "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SchemaVersion: 1,
			CapturedAt:    time.Now().UTC(),
		}
	}

	cases := []struct {
		name     string
		mutate   func(*state.OpenAPISnapshot)
		wantSubs string
	}{
		{
			name:     "empty deployment_id",
			mutate:   func(s *state.OpenAPISnapshot) { s.DeploymentID = "" },
			wantSubs: "empty deployment_id",
		},
		{
			name:     "empty app_id",
			mutate:   func(s *state.OpenAPISnapshot) { s.AppID = "" },
			wantSubs: "empty app_id",
		},
		{
			name:     "empty scope",
			mutate:   func(s *state.OpenAPISnapshot) { s.Scope = "" },
			wantSubs: "empty scope",
		},
		{
			name:     "empty snapshot bytes",
			mutate:   func(s *state.OpenAPISnapshot) { s.Snapshot = nil },
			wantSubs: "empty snapshot bytes",
		},
		{
			name:     "empty sha256",
			mutate:   func(s *state.OpenAPISnapshot) { s.SHA256 = "" },
			wantSubs: "empty sha256",
		},
		{
			name:     "schema_version zero",
			mutate:   func(s *state.OpenAPISnapshot) { s.SchemaVersion = 0 },
			wantSubs: "schema_version must be >= 1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := good()
			tc.mutate(&snap)
			err := s.UpdateDeploymentOpenAPISnapshot(ctx, snap)
			if err == nil {
				t.Fatalf("UpdateDeploymentOpenAPISnapshot(%s) = nil err; want validation error", tc.name)
			}
			if !regexp.MustCompile(tc.wantSubs).MatchString(err.Error()) {
				t.Errorf("err = %q; want substring %q", err.Error(), tc.wantSubs)
			}
		})
	}
}

// TestPg_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_CapturedAtDefaults
// pins the CapturedAt zero-default branch in
// [upsertDeploymentOpenAPISnapshotDBTX] (pgstore.go line 5384):
// when the caller omits CapturedAt, the helper substitutes
// time.Now().UTC() before the SQL write. The MemStore mirror
// has the same fallback (pinned by memstore_openapi_capture_test.go);
// this test pins the SQL mirror against a real cluster.
func TestPg_OpenAPICapture_UpdateDeploymentOpenAPISnapshot_CapturedAtDefaults(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	snap := state.OpenAPISnapshot{
		DeploymentID:  "33333333-3333-3333-3333-333333333333",
		AppID:         "22222222-2222-2222-2222-222222222222",
		Scope:         "prod",
		Snapshot:      []byte(`{"captured_at_default":true}`),
		SHA256:        "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		SchemaVersion: 1,
		// CapturedAt deliberately zero.
	}
	before := time.Now().UTC()
	if err := s.UpdateDeploymentOpenAPISnapshot(ctx, snap); err != nil {
		t.Fatalf("UpdateDeploymentOpenAPISnapshot = %v; want nil", err)
	}
	after := time.Now().UTC()

	got, err := s.OpenAPISnapshotByDeployment(ctx, snap.DeploymentID)
	if err != nil {
		t.Fatalf("OpenAPISnapshotByDeployment = %v; want nil", err)
	}
	if got.CapturedAt.Before(before) || got.CapturedAt.After(after) {
		t.Errorf("CapturedAt = %v; want in [%v, %v]", got.CapturedAt, before, after)
	}
}

// TestPg_OpenAPICapture_MarkDeploymentLive_NotFound pins the
// ErrNotFound branch of [PgStore.MarkDeploymentLive] (pgstore.go
// line 5315): the tx status-read on a missing deployment id
// returns ErrNotFound so callers (imaged, builderd, schedd)
// surface a clear "unknown deployment" error rather than a
// raw SQL message.
func TestPg_OpenAPICapture_MarkDeploymentLive_NotFound(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if err := s.MarkDeploymentLive(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("MarkDeploymentLive(missing) = %v; want ErrNotFound", err)
	}
}

// TestPg_OpenAPICapture_MarkDeploymentLive_InvalidUUID pins
// the uuid.Parse error branch of [PgStore.MarkDeploymentLive]
// (pgstore.go line 5322-5324): a non-UUID id is rejected
// before the SQL runs so a malformed producer-side call
// surfaces a clean error rather than a pgx parse panic.
func TestPg_OpenAPICapture_MarkDeploymentLive_InvalidUUID(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	err := s.MarkDeploymentLive(ctx, "not-a-uuid")
	if err == nil {
		t.Fatalf("MarkDeploymentLive(not-a-uuid) = nil; want parse error")
	}
	if !regexp.MustCompile(`parse deployment`).MatchString(err.Error()) {
		t.Errorf("err = %q; want substring %q", err.Error(), "parse deployment")
	}
}

// TestPg_OpenAPICapture_LatestForScope_NotFound pins the
// not-found branch of [PgStore.LatestOpenAPISnapshotForScope]:
// a scope with no captured snapshot returns ErrNotFound so
// PR-C's gate takes the "no baseline" branch and lets the
// first promotion through unblocked.
func TestPg_OpenAPICapture_LatestForScope_NotFound(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := s.LatestOpenAPISnapshotForScope(ctx, "44444444-4444-4444-4444-444444444444", "prod"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("LatestOpenAPISnapshotForScope(missing) = %v; want ErrNotFound", err)
	}
}

// TestPg_OpenAPICapture_OpenAPISnapshotByDeployment_NotFound
// pins the not-found branch of
// [PgStore.OpenAPISnapshotByDeployment]: a deployment id with
// no captured snapshot returns ErrNotFound.
func TestPg_OpenAPICapture_OpenAPISnapshotByDeployment_NotFound(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	if _, err := s.OpenAPISnapshotByDeployment(ctx, "55555555-5555-5555-5555-555555555555"); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("OpenAPISnapshotByDeployment(missing) = %v; want ErrNotFound", err)
	}
}
