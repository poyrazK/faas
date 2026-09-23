//go:build !no_pg

// pgstore_sidecars_test.go — round-trip tests for the sidecars
// jsonb column added by migration 00118 (issue #463 / ADR-068).
//
// Build tag: !no_pg matches the rest of the pgstore-side tests; set
// FAAS_SKIP_PG_TESTS=1 to opt out locally without rebuilding.
package state_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSidecarsFixture creates an account + app for sidecar round-trip
// tests against a real Postgres schema. The deployment is created
// separately by the test so the test can pin the Sidecars raw bytes
// verbatim. The pool is returned so the capacity rejection test can
// run the raw INSERT against the same schema the app was created on.
func pgSidecarsFixture(t *testing.T) (*state.PgStore, context.Context, state.App, *pgxpool.Pool) {
	t.Helper()
	s, pool, ctx := pgStoreWithPool(t)
	account, err := s.CreateAccount(ctx, "pg-sidecars-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{
		AccountID: account.ID, Slug: "pg-sidecars-" + uuid.NewString(),
		Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, ctx, app, pool
}

// TestPgStore_Deployment_Sidecars_JSONRoundTrip pins that the
// 2-sidecar JSONB payload survives CreateDeployment ↔ DeploymentByID
// byte-for-byte (PR-A's contract). The capacity end of the contract is
// pinned by the migration test (deployments_sidecars_test.go); this
// test pins the byte-level round-trip on the most common shape.
func TestPgStore_Deployment_Sidecars_JSONRoundTrip(t *testing.T) {
	s, ctx, app, _ := pgSidecarsFixture(t)
	raw := json.RawMessage(`[
		{"name":"migrator","image":"ghcr.io/me/migrator@sha256:0000000000000000000000000000000000000000000000000000000000000001","type":"init","cmd":["--to","head"]},
		{"name":"scraper","image":"ghcr.io/me/scraper@sha256:0000000000000000000000000000000000000000000000000000000000000002","type":"sidecar"}
	]`)

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:sidecars",
		Sidecars:    raw,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	// PG's jsonb normalises key order + whitespace; a raw
	// bytes.Equal never holds. We decode both sides through
	// json.Unmarshal and compare with reflect.DeepEqual, which
	// is correct for the JSON value semantics (unordered keys,
	// numeric equivalence, …). The `interface{} == interface{}`
	// shortcut panics on uncomparable kinds (slices, maps).
	if !reflect.DeepEqual(decodeJSONB(t, got.Sidecars), decodeJSONB(t, raw)) {
		t.Errorf("Sidecars round-trip drifted\n  got:  %s\n  want: %s", got.Sidecars, raw)
	}
}

// TestPgStore_Deployment_Sidecars_NilDefaultsToEmptyArray pins the
// "missing field" path. CreateDeployment with a nil Sidecars must
// store the cell as `[]` (NOT NULL DEFAULT '[]'::jsonb on the
// column, plus the `notNullEmptyJSONRaw` helper on the write path).
// The read-side `coalesce(sidecars, '[]'::jsonb)` projection
// guarantees the empty-array shape reads back even from a NULL
// column — but in the PR-A path the helper writes the literal
// `[]` so the read-back is the same `[]` byte-for-byte.
//
// This is the load-bearing test for the "no sidecars submitted"
// branch: the wire shape is `[]` to the customer, and the
// persisted shape is `[]` to PR-B.
func TestPgStore_Deployment_Sidecars_NilDefaultsToEmptyArray(t *testing.T) {
	s, ctx, app, _ := pgSidecarsFixture(t)

	dep, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:no-sidecars",
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}

	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil {
		t.Fatalf("DeploymentByID: %v", err)
	}
	if !reflect.DeepEqual(decodeJSONB(t, got.Sidecars), []interface{}{}) {
		t.Errorf("Sidecars default = %s; want []", got.Sidecars)
	}
}

// decodeJSONB unmarshals a jsonb raw payload into an
// `interface{}` so the round-trip and default assertions can use
// reflect.DeepEqual (PG's jsonb normalises key order + whitespace,
// which makes bytes.Equal a wrong primitive here).
func decodeJSONB(t *testing.T, raw []byte) interface{} {
	t.Helper()
	var out interface{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode JSONB %q: %v", raw, err)
	}
	return out
}

// TestPgStore_Deployment_Sidecars_FiveCapRejection pins the schema-level
// cap. The API gate (Sidecars.Validate) is the load-bearing five-helper
// guard, but the schema CHECK is the second-line defence. A
// hand-call to the store layer that bypasses the API gate (manual
// SQL, future grpc handler, debug shell) must still trip the cap.
//
// Pinned via raw pool.Exec, not s.CreateDeployment (the latter
// would short-circuit on the notNullEmptyJSONRaw write path).
func TestPgStore_Deployment_Sidecars_FiveCapRejection(t *testing.T) {
	s, ctx, app, pool := pgSidecarsFixture(t)

	if _, err := s.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindImage,
		ImageDigest: "sha256:setup-cap",
	}); err != nil {
		t.Fatalf("setup CreateDeployment: %v", err)
	}

	// Now attempt a 6-helper INSERT via the raw pool, bypassing
	// the store-layer notNullEmptyJSONRaw normalisation. This
	// round-trips through the exact column shape the migration
	// created. The CHECK constraint must reject with 23514.
	if _, err := pool.Exec(ctx,
		`insert into deployments (app_id, image_digest, kind, status, sidecars)
		 values ($1, 'sha256:over-cap', 'image', 'pending',
		         '[
		           {"name":"a","image":"x@sha256:01","type":"sidecar"},
		           {"name":"b","image":"x@sha256:02","type":"sidecar"},
		           {"name":"c","image":"x@sha256:03","type":"sidecar"},
		           {"name":"d","image":"x@sha256:04","type":"sidecar"},
		           {"name":"e","image":"x@sha256:05","type":"sidecar"},
		           {"name":"f","image":"x@sha256:06","type":"sidecar"}
		         ]'::jsonb)`,
		app.ID,
	); err == nil {
		t.Errorf("6-helper INSERT: got no error; want CHECK cap violation (23514)")
	}
}
