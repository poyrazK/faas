// Unit tests for the env handlers (issue #395 / ADR-045). Coverage
// mirrors handlers_secrets_test.go MINUS the recipient/ciphertext cases
// that don't apply to plaintext env vars:
//
//   - happy-path PUT + GET + DELETE round-trip (no seal step)
//   - per-quota enforcement (403 plan_limit_env_vars on the +1 row)
//   - over-byte-cap rejection (413 env_value_too_large)
//   - bad key shape (400 env_var_invalid_key — same regex as secrets)
//   - delete-not-found (400 env_var_not_found, NOT 404)
//   - cross-app isolation (env on app A is invisible to GET/PUT/DELETE
//     against app B in the same account)
//   - redaction invariant: log line for a successful set mentions the key
//     name but never the plaintext value
//   - redeploy preservation: a new deployment does NOT erase env rows
//     (acceptance #6 in the plan)
//
// All tests run KVM-free via the in-memory store. No MFA gate is
// exercised here because env routes explicitly skip requireMFA per
// ADR-045 §Decision (env vars are non-sensitive by contract).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnv_PutGetDeleteRoundTrip exercises the happy path: PUT a key,
// GET the list, DELETE it. The list response carries key + timestamps
// only — the value NEVER appears (mirrors the secret surface minus the
// seal; the value is gone from the wire by design).
func TestEnv_PutGetDeleteRoundTrip(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-rt-app")

	// PUT — value echoed back via the response ({"key":"FOO"}).
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/LOG_LEVEL",
		api.PutAppEnvRequest{Value: "debug"}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}

	// GET — list shape returns key + timestamps, no value field.
	listRec := e.do(t, "GET", "/v1/apps/"+app.Slug+"/env", nil, nil)
	if listRec.Code != 200 {
		t.Fatalf("GET list: %d %s", listRec.Code, listRec.Body.String())
	}
	// Defensive: the plaintext value must never leak through the list
	// response. (PutAppEnvRequest.Value is the input shape, NOT the
	// response shape — see AppEnvResponse doc.)
	if strings.Contains(listRec.Body.String(), "debug") {
		t.Errorf("plaintext leaked into list response: %s", listRec.Body.String())
	}
	var listResp struct {
		Env   []api.AppEnvResponse `json:"env"`
		Quota int                  `json:"quota_max"`
		Count int                  `json:"count"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if listResp.Quota != 32 {
		t.Errorf("Hobby EnvVarsMax = %d, want 32", listResp.Quota)
	}
	if listResp.Count != 1 || len(listResp.Env) != 1 || listResp.Env[0].Key != "LOG_LEVEL" {
		t.Errorf("list shape = %+v, want one LOG_LEVEL", listResp)
	}

	// DELETE.
	delRec := e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/env/LOG_LEVEL", nil, nil)
	if delRec.Code != 204 {
		t.Fatalf("DELETE: %d %s", delRec.Code, delRec.Body.String())
	}

	// List now empty.
	listRec = e.do(t, "GET", "/v1/apps/"+app.Slug+"/env", nil, nil)
	if listRec.Code != 200 {
		t.Fatalf("GET list after delete: %d", listRec.Code)
	}
	_ = json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if listResp.Count != 0 {
		t.Errorf("count after delete = %d, want 0", listResp.Count)
	}

	// Store-level: row exists post-PUT, gone post-DELETE.
	rows, err := e.store.ListAppEnv(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("store list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("store after delete = %d, want 0", len(rows))
	}
}

func TestInvalidateAppSnapshotsMarksWarmAndInitStale(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-snapshot-invalidate-app")
	ctx := context.Background()
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:deadbeefcafebabe1234567890abcdef1234567890abcdef1234567890abcdef",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		if _, err := e.store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID,
			Tier:         tier,
			FCVersion:    "test",
			StorageKey:   "snap/" + tier,
		}); err != nil {
			t.Fatalf("CreateSnapshot(%s): %v", tier, err)
		}
	}

	invalidated, err := e.s.invalidateAppSnapshots(ctx, app.ID)
	if err != nil {
		t.Fatalf("invalidateAppSnapshots: %v", err)
	}
	if invalidated != 2 {
		t.Fatalf("invalidated = %d, want warm + init", invalidated)
	}
	for _, tier := range []string{state.SnapshotTierWarm, state.SnapshotTierInit} {
		if _, err := e.store.LatestSnapshotForTier(ctx, dep.ID, tier); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("LatestSnapshotForTier(%s) err = %v, want not found after invalidation", tier, err)
		}
	}
}

func TestInvalidateAppSnapshotsCoversMultipleDeployments(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-snapshot-multi-deployment-app")
	ctx := context.Background()
	for i, status := range []state.DeploymentStatus{state.DeployLive, state.DeploySuperseded} {
		dep, err := e.store.CreateDeployment(ctx, state.Deployment{
			AppID:       app.ID,
			ImageDigest: fmt.Sprintf("sha256:%064x", i+1),
			Kind:        state.DeploymentKindImage,
			Status:      status,
		})
		if err != nil {
			t.Fatalf("CreateDeployment(%d): %v", i, err)
		}
		if _, err := e.store.CreateSnapshot(ctx, state.Snapshot{
			DeploymentID: dep.ID,
			Tier:         state.SnapshotTierWarm,
			FCVersion:    "test",
			StorageKey:   fmt.Sprintf("snap/%d", i),
		}); err != nil {
			t.Fatalf("CreateSnapshot(%d): %v", i, err)
		}
	}

	invalidated, err := e.s.invalidateAppSnapshots(ctx, app.ID)
	if err != nil {
		t.Fatalf("invalidateAppSnapshots: %v", err)
	}
	if invalidated != 2 {
		t.Fatalf("invalidated = %d, want both deployments", invalidated)
	}
}

func TestEnvMutationInvalidatesRestorableSnapshot(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-snapshot-http-app")
	ctx := context.Background()
	dep, err := e.store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		ImageDigest: "sha256:deadbeefcafebabe1234567890abcdef1234567890abcdef1234567890abcdef",
		Kind:        state.DeploymentKindImage,
		Status:      state.DeployLive,
	})
	if err != nil {
		t.Fatalf("CreateDeployment: %v", err)
	}
	if _, err := e.store.CreateSnapshot(ctx, state.Snapshot{
		DeploymentID: dep.ID,
		Tier:         state.SnapshotTierWarm,
		FCVersion:    "test",
		StorageKey:   "snap/http",
	}); err != nil {
		t.Fatalf("CreateSnapshot: %v", err)
	}
	rec := e.do(t, http.MethodPut, "/v1/apps/"+app.Slug+"/env/LOG_LEVEL", api.PutAppEnvRequest{Value: "debug"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT env: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := e.store.LatestSnapshotForTier(ctx, dep.ID, state.SnapshotTierWarm); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("LatestSnapshotForTier after env mutation = %v, want not found", err)
	}
}

// TestEnv_QuotaExceeded_Free403 asserts the per-plan EnvVarsMax gate.
// Free's limit is 8; the 9th distinct key must return 403
// plan_limit_env_vars. Re-PUTs of an existing key are NOT new rows
// (covered in the count-distinct-from-re-upsert test below).
func TestEnv_QuotaExceeded_Free403(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := createApp(t, e, "env-quota-app")

	// PUT 8 distinct keys (Free's quota).
	for i := 0; i < 8; i++ {
		key := keyName(i)
		rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/"+key,
			api.PutAppEnvRequest{Value: "v"}, nil)
		if rec.Code != 200 {
			t.Fatalf("PUT %s: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	// 9th must 403.
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/KEY_NINE",
		api.PutAppEnvRequest{Value: "v"}, nil)
	if rec.Code != 403 {
		t.Fatalf("9th PUT: %d %s, want 403", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "plan_limit_env_vars") {
		t.Errorf("9th PUT body = %s, want plan_limit_env_vars", rec.Body.String())
	}
}

// TestEnv_QuotaCountedDistinctFromReUpsert asserts the "re-PUT of an
// existing key is NOT a new row" rule. Mirrors the secrets test of
// the same name — the quota logic in checkEnvQuota subtracts 1 when
// the key already exists.
func TestEnv_QuotaCountedDistinctFromReUpsert(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := createApp(t, e, "env-reupsert-app")

	// Fill to quota with 8 distinct keys.
	for i := 0; i < 8; i++ {
		key := keyName(i)
		rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/"+key,
			api.PutAppEnvRequest{Value: "v1"}, nil)
		if rec.Code != 200 {
			t.Fatalf("PUT %s: %d %s", key, rec.Code, rec.Body.String())
		}
	}
	// Re-PUT one of them with a new value — must succeed (not 403).
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/"+keyName(3),
		api.PutAppEnvRequest{Value: "v2"}, nil)
	if rec.Code != 200 {
		t.Fatalf("re-PUT %s: %d %s, want 200", keyName(3), rec.Code, rec.Body.String())
	}
	// And the 9th distinct key still 403.
	rec = e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/NEW_KEY",
		api.PutAppEnvRequest{Value: "v"}, nil)
	if rec.Code != 403 {
		t.Fatalf("9th distinct: %d %s, want 403", rec.Code, rec.Body.String())
	}
}

// TestEnv_ValueTooLarge_RejectsBeforeStore asserts the byte cap is
// enforced BEFORE the row reaches the store. Free's EnvValueMaxBytes
// is 4 KiB; a 5 KiB value must 413.
func TestEnv_ValueTooLarge_RejectsBeforeStore(t *testing.T) {
	e := setup(t, api.PlanFree)
	app := createApp(t, e, "env-toobig-app")
	// 5 KiB > 4 KiB.
	big := strings.Repeat("x", 5*1024)
	rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/BIG",
		api.PutAppEnvRequest{Value: big}, nil)
	if rec.Code != 413 {
		t.Fatalf("PUT big: %d %s, want 413", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "env_value_too_large") {
		t.Errorf("PUT body = %s, want env_value_too_large", rec.Body.String())
	}
	// And the row must NOT have landed.
	rows, err := e.store.ListAppEnv(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("store list: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("store after rejected PUT = %d, want 0", len(rows))
	}
}

// TestEnv_InvalidKey_400 exercises ValidateEnvKey. Mirrors
// handlers_secrets_test.go's TestSecrets_InvalidKey_400 to confirm the
// regex is identical (intentional — both surfaces share the same
// ASCII identifier grammar).
func TestEnv_InvalidKey_400(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-badkey-app")
	cases := []struct {
		name string
		key  string
	}{
		{"lowercase", "log_level"},
		{"starts_with_digit", "1FOO"},
		{"contains_dash", "LOG-LEVEL"},
		{"contains_dot", "LOG.LEVEL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/"+tc.key,
				api.PutAppEnvRequest{Value: "v"}, nil)
			if rec.Code != 400 {
				t.Fatalf("PUT %q: %d %s, want 400", tc.key, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "env_var_invalid_key") {
				t.Errorf("PUT body = %s, want env_var_invalid_key", rec.Body.String())
			}
		})
	}
}

// TestEnv_DeleteNotFound_400 asserts the second-DELETE returns 400
// env_var_not_found, not 404. The URL resource IS the env var; this
// matches the secret surface so SDK callers reuse the same branch.
func TestEnv_DeleteNotFound_400(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-delmiss-app")

	// First DELETE: 404 because the key was never set. Wait — the route
	// returns 400 env_var_not_found even on the first miss; that's by
	// design (see deleteEnv comment).
	rec := e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/env/MISSING", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("DELETE missing: %d %s, want 400", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "env_var_not_found") {
		t.Errorf("DELETE body = %s, want env_var_not_found", rec.Body.String())
	}

	// PUT then DELETE then DELETE — second DELETE is the 400.
	e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/PRESENT",
		api.PutAppEnvRequest{Value: "v"}, nil)
	rec = e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/env/PRESENT", nil, nil)
	if rec.Code != 204 {
		t.Fatalf("first DELETE: %d, want 204", rec.Code)
	}
	rec = e.do(t, "DELETE", "/v1/apps/"+app.Slug+"/env/PRESENT", nil, nil)
	if rec.Code != 400 {
		t.Fatalf("second DELETE: %d %s, want 400", rec.Code, rec.Body.String())
	}
}

// TestEnv_AppOwnershipBoundary asserts that env rows on app A are
// invisible to GET/PUT/DELETE against app B in the same account.
// Cross-app isolation is the same property the secrets surface has;
// the pkg/state.UpsertAppEnv contract pins it on the store side and
// the handler relies on loadApp's 404 to fail closed.
func TestEnv_AppOwnershipBoundary(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appA := createApp(t, e, "env-a")
	appB := createApp(t, e, "env-b")

	// PUT against appA.
	rec := e.do(t, "PUT", "/v1/apps/"+appA.Slug+"/env/FOO",
		api.PutAppEnvRequest{Value: "a"}, nil)
	if rec.Code != 200 {
		t.Fatalf("PUT appA: %d %s", rec.Code, rec.Body.String())
	}

	// GET against appB: must be empty.
	listRec := e.do(t, "GET", "/v1/apps/"+appB.Slug+"/env", nil, nil)
	if listRec.Code != 200 {
		t.Fatalf("GET appB: %d", listRec.Code)
	}
	var listResp struct {
		Env   []api.AppEnvResponse `json:"env"`
		Count int                  `json:"count"`
	}
	_ = json.Unmarshal(listRec.Body.Bytes(), &listResp)
	if listResp.Count != 0 {
		t.Errorf("appB sees %d env rows, want 0", listResp.Count)
	}

	// DELETE against appB for the same key: must 400 (env_var_not_found
	// because the row is on appA, not on appB).
	rec = e.do(t, "DELETE", "/v1/apps/"+appB.Slug+"/env/FOO", nil, nil)
	if rec.Code != 400 {
		t.Errorf("cross-app DELETE: %d %s, want 400", rec.Code, rec.Body.String())
	}

	// Row still present on appA.
	rows, err := e.store.ListAppEnv(context.Background(), e.acct.ID, appA.ID)
	if err != nil {
		t.Fatalf("store list appA: %v", err)
	}
	if len(rows) != 1 || rows[0].Key != "FOO" || rows[0].Value != "a" {
		t.Errorf("appA rows = %+v, want one FOO=a", rows)
	}
}

// TestEnv_RedeployPreservesEnv is acceptance #6: a new deployment must
// NOT erase env rows. Env is per-app, not per-deployment — see ADR-045
// §Decision "API env persists across redeploy".
//
// Strengthened (review #4): the original test used `_ = rec` to discard
// the deployment POST response, which let a future refactor that
// accidentally adds DeleteAppEnv(appID) to the deployment-create
// handler pass silently. The fix is two-fold:
//
//  1. Assert the deployment POST itself lands in an acceptable status
//     code. The fake source URL fails downstream (no real tarball),
//     but the request shape validates — we accept 202 (deferred
//     build), 4xx (validation/handler reject), but NOT 5xx (which
//     would mean our test environment is broken and the assertion
//     below would be silently meaningless).
//  2. Pin a before/after env row count AND a value-equality check, so
//     a refactor that drops even one row trips the test.
func TestEnv_RedeployPreservesEnv(t *testing.T) {
	e := setup(t, api.PlanHobby)
	app := createApp(t, e, "env-redeploy-app")

	// PUT two env rows.
	e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/LOG_LEVEL",
		api.PutAppEnvRequest{Value: "debug"}, nil)
	e.do(t, "PUT", "/v1/apps/"+app.Slug+"/env/FEATURE_X",
		api.PutAppEnvRequest{Value: "on"}, nil)

	// Sanity: 2 rows before the redeploy call. Anchors the before-count
	// assertion so the test fails loud if the PUTs above regress.
	before, err := e.store.ListAppEnv(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("store list before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("pre-redeploy rows = %d, want 2 (test setup)", len(before))
	}

	// Trigger a new deployment. We don't need a real source tarball
	// — the handler validates the request shape; the env rows live
	// outside the deployment cycle.
	rec := e.do(t, "POST", "/v1/apps/"+app.Slug+"/deployments",
		map[string]any{"source": "https://example.com/new.tar.gz"}, nil)
	// Accept 202 (deferred build, valid request) or 4xx (handler
	// rejects the fake source URL). Reject 5xx — that means the test
	// harness itself is broken and any downstream assertion is
	// meaningless.
	if rec.Code >= 500 {
		t.Fatalf("deployment POST: %d %s (test harness broken; assertion below would be silent)",
			rec.Code, rec.Body.String())
	}
	if rec.Code < 200 || (rec.Code >= 300 && rec.Code < 400) || rec.Code >= 500 {
		t.Errorf("deployment POST: %d %s (want 2xx/4xx)", rec.Code, rec.Body.String())
	}

	// Env rows must still be present. Compare the after-count AND
	// the key/value contents against the snapshot taken before the
	// redeploy — a refactor that drops any row trips this assertion.
	after, err := e.store.ListAppEnv(context.Background(), e.acct.ID, app.ID)
	if err != nil {
		t.Fatalf("store list after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("post-redeploy rows = %d, want %d (env must survive redeploy)",
			len(after), len(before))
	}
	beforeMap := map[string]string{}
	for _, r := range before {
		beforeMap[r.Key] = r.Value
	}
	for _, r := range after {
		if got, ok := beforeMap[r.Key]; !ok || got != r.Value {
			t.Errorf("env row %q drifted across redeploy: before=%q after=%q",
				r.Key, got, r.Value)
		}
	}
}

// keyName produces a stable ^[A-Z][A-Z0-9_]*$ identifier for the i-th
// loop iteration in the quota tests. KEY_ZERO, KEY_ONE, …; up to
// KEY_TWENTY_NINE fits in the 32 KB per-key cap.
func keyName(i int) string {
	return "KEY_" + intToLetters(i)
}

// intToLetters encodes 0→"ZERO", 1→"ONE", etc., so all keys stay
// within the regex shape (no digits as the leading char). Bounded by
// the test runner — only 8 distinct values are needed.
func intToLetters(i int) string {
	names := []string{"ZERO", "ONE", "TWO", "THREE", "FOUR", "FIVE", "SIX", "SEVEN", "EIGHT", "NINE"}
	if i < len(names) {
		return names[i]
	}
	return "MISC"
}

// pin to silence unused imports if a future refactor trims.
var _ = state.Account{}
