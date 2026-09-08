// secrets_scope_e2e_test.go — ADR-092 PR-B customer-facing wire-surface
// acceptance for the new ?scope= thread on secrets routes.
//
// Eleven assertions, in order:
//
//   1.  Seed the Free-plan app + per-app-across-all-scopes cap (8).
//   2.  PUT secret "ALPHA" at scope=prod.
//   3.  PUT secret "ALPHA" at scope=staging.
//   4.  PUT secret "BETA"  at scope=default.
//   5.  GET ?scope=prod      — must contain ALPHA, must NOT contain BETA.
//   6.  GET ?scope=staging   — must contain ALPHA, must NOT contain BETA.
//   7.  GET ?scope=default   — must contain BETA, must NOT contain ALPHA.
//   8.  PUT ?scope=__all__   — server rejects with 400 env_scope_reserved.
//   9.  PUT ?scope=NOT-A-valid-scope! — server rejects with 400 env_scope_invalid.
//  10.  GET ?scope=__all__   — nested secrets_by_scope map carries all three
//                              scopes, each with the right keys.
//  11.  Quota: 9th secret on Free (cap 8 across all scopes) → 403 plan_limit_secrets.
//
// Why this is KVM-free: apid owns the secret row + scope query; the wire
// surface is HTTP. schedd/vmmd are not in the loop, so the wake-paths
// exercised by TestScopeMountedSecrets are covered separately by the metal
// suite (the same split TestSecretsMatrixPg documents).
//
// The quota-counts-across-scopes assertion (11) is the load-bearing one:
// it pins the pkg/api/limits.go::SecretCountMax posture (per-app-across-all-
// scopes) at the wire surface, not just in code comments. Without it the
// quota posture could regress silently in a future PR.

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestSecretsScopeSurfacePg is the ADR-092 PR-B wire-surface acceptance test.
// It exercises the eleven assertions listed in the file header in order —
// keeping the order stable is important because each assertion leaves state
// (rows in app_secrets) the next assertion depends on, and we want a flake
// on assertion N to leave enough context for the operator to see what N-1
// looked like.
func TestSecretsScopeSurfacePg(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Both recipient (public half apid seals against) and identity (private
	// half apid uses for kid-stamping) are required because PUT goes through
	// the kid-stamping path added in PR-089 PR-C. Without the pair the PUT
	// would 503 on the "host age identities not loaded" path and the test
	// would assert the wrong surface. Same shape as secrets_e2e_test.go.
	tmpDir := t.TempDir()
	recipientPath := filepath.Join(tmpDir, "host.age.pub")
	identityPath := recipientPath + ".priv"
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatalf("write recipient: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + identityPath,
	})

	// Assertion 1: seed Free account + app + record the cap (8 across scopes).
	const plan = api.PlanFree
	key := h.SeedAccount(context.Background(), plan, "scope-surface")
	limits := api.MustLimitsFor(plan)
	if limits.SecretCountMax < 8 {
		t.Fatalf("Free SecretCountMax=%d, want >=8 for this test", limits.SecretCountMax)
	}
	const slug = "scope-surf-app"
	if code := statusOnly(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug}); code != http.StatusCreated {
		t.Fatalf("create app: %d", code)
	}

	// Assertion 2: PUT ALPHA at scope=prod.
	putAt := func(scope, keyName, value string) {
		t.Helper()
		path := "/v1/apps/" + slug + "/secrets/" + keyName
		if scope != "" {
			path += "?scope=" + url.QueryEscape(scope)
		}
		body := api.PutAppSecretRequest{Value: value}
		if code := statusOnly(t, h, key, http.MethodPut, path, body); code != http.StatusOK {
			t.Fatalf("PUT %s scope=%q: %d", keyName, scope, code)
		}
	}
	putAt("prod", "ALPHA", "v-prod-alpha")
	// Assertion 3: PUT ALPHA at scope=staging (same key, different scope).
	putAt("staging", "ALPHA", "v-stg-alpha")
	// Assertion 4: PUT BETA at scope=default.
	putAt("", "BETA", "v-default-beta")

	// getAt returns the parsed AppSecretListResponse for the given scope
	// query. Used by assertions 5-7 (per-scope GET) and assertion 10 (__all__).
	getAt := func(scope string) api.AppSecretListResponse {
		t.Helper()
		path := "/v1/apps/" + slug + "/secrets"
		if scope != "" {
			path += "?scope=" + url.QueryEscape(scope)
		}
		raw, status := doReq(t, h, key, http.MethodGet, path, nil)
		if status != http.StatusOK {
			t.Fatalf("GET scope=%q: %d (body=%s)", scope, status, raw)
		}
		var out api.AppSecretListResponse
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode AppSecretListResponse scope=%q: %v (body=%s)", scope, err, raw)
		}
		return out
	}
	containsKey := func(rows []api.AppSecretResponse, want string) bool {
		for _, r := range rows {
			if r.Key == want {
				return true
			}
		}
		return false
	}
	// rawBytesFor issues the GET and returns the raw response body so the
	// plaintext-leak check can scan for the literal value. AppSecretResponse
	// has no Ciphertext field (redaction invariant — the ciphertext is
	// NEVER on the wire; sealed-server-side, returned-on-rotate only), so
	// the leak check has to operate on the raw bytes, the same posture as
	// secrets_e2e_test.go::TestSecretsMatrixPg/happy_path.
	rawBytesFor := func(scope string) []byte {
		t.Helper()
		path := "/v1/apps/" + slug + "/secrets"
		if scope != "" {
			path += "?scope=" + url.QueryEscape(scope)
		}
		raw, _ := doReq(t, h, key, http.MethodGet, path, nil)
		return raw
	}

	// Assertion 5: GET ?scope=prod — ALPHA present, BETA absent, plaintext absent.
	respProd := getAt("prod")
	if !containsKey(respProd.Secrets, "ALPHA") {
		t.Errorf("scope=prod: missing ALPHA (got %d rows)", len(respProd.Secrets))
	}
	if containsKey(respProd.Secrets, "BETA") {
		t.Errorf("scope=prod: BETA leaked from default scope")
	}
	if strings.Contains(string(rawBytesFor("prod")), "v-prod-alpha") {
		t.Errorf("scope=prod: plaintext leaked in GET response")
	}
	for _, r := range respProd.Secrets {
		if r.Scope != "prod" {
			t.Errorf("scope=prod: row %q has scope=%q, want prod", r.Key, r.Scope)
		}
	}

	// Assertion 6: GET ?scope=staging — ALPHA present (its staging variant),
	// BETA absent, plaintext absent.
	respStg := getAt("staging")
	if !containsKey(respStg.Secrets, "ALPHA") {
		t.Errorf("scope=staging: missing ALPHA (got %d rows)", len(respStg.Secrets))
	}
	if containsKey(respStg.Secrets, "BETA") {
		t.Errorf("scope=staging: BETA leaked from default scope")
	}
	if strings.Contains(string(rawBytesFor("staging")), "v-stg-alpha") {
		t.Errorf("scope=staging: plaintext leaked in GET response")
	}
	for _, r := range respStg.Secrets {
		if r.Scope != "staging" {
			t.Errorf("scope=staging: row %q has scope=%q, want staging", r.Key, r.Scope)
		}
	}

	// Assertion 7: GET ?scope=default — BETA present, ALPHA absent (strict
	// per-scope resolution; no silent default overlay from prod/staging).
	respDef := getAt("default")
	if !containsKey(respDef.Secrets, "BETA") {
		t.Errorf("scope=default: missing BETA (got %d rows)", len(respDef.Secrets))
	}
	if containsKey(respDef.Secrets, "ALPHA") {
		t.Errorf("scope=default: ALPHA leaked from prod/staging scopes")
	}
	for _, r := range respDef.Secrets {
		if r.Scope != "default" {
			t.Errorf("scope=default: row %q has scope=%q, want default", r.Key, r.Scope)
		}
	}

	// Assertion 8: PUT ?scope=__all__ → 400 env_scope_reserved.
	// The reserved sentinel is rejected on writes because it has no
	// meaning on a single-row write. Same posture as the env route
	// (ADR-090 D7) — env_scope_reserved is reused, no secret_scope_*
	// minted (per plan §Decisions).
	assertProblemAPID(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/secrets/EXTRA?scope=__all__",
		api.PutAppSecretRequest{Value: "v"},
		http.StatusBadRequest,
		api.CodeEnvScopeReserved)

	// Assertion 9: PUT ?scope=NOT-A-valid-scope! → 400 env_scope_invalid.
	// The scope shape check is enforced before any DB work, so this
	// runs on the main app without needing a
	// fresh one — invalid-shape failures don't count against quota.
	assertProblemAPID(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/secrets/EXTRA?scope=NOT-A-valid-scope!",
		api.PutAppSecretRequest{Value: "v"},
		http.StatusBadRequest,
		api.CodeEnvScopeInvalid)

	// Assertion 10: GET ?scope=__all__ — nested secrets_by_scope map
	// carries all three scopes, each with the right key. The flat
	// `secrets` arm is empty on the __all__ path (discriminated union
	// posture; ADR-090 D3 + ADR-092 mirror).
	respAll := getAt("__all__")
	if len(respAll.Secrets) != 0 {
		t.Errorf("scope=__all__: flat secrets arm should be empty (discriminated union), got %d rows",
			len(respAll.Secrets))
	}
	if len(respAll.SecretsByScope) != 3 {
		t.Errorf("scope=__all__: secrets_by_scope has %d scopes, want 3 (default, prod, staging)",
			len(respAll.SecretsByScope))
	}
	wantKeysByScope := map[string][]string{
		"default": {"BETA"},
		"prod":    {"ALPHA"},
		"staging": {"ALPHA"},
	}
	for scope, wantKeys := range wantKeysByScope {
		rows, ok := respAll.SecretsByScope[scope]
		if !ok {
			t.Errorf("scope=__all__: missing %q in secrets_by_scope", scope)
			continue
		}
		got := map[string]bool{}
		for _, r := range rows {
			got[r.Key] = true
			if r.Scope != scope {
				t.Errorf("scope=__all__: row %q in %q bucket has scope=%q",
					r.Key, scope, r.Scope)
			}
		}
		for _, want := range wantKeys {
			if !got[want] {
				t.Errorf("scope=__all__: %q scope missing key %q (got %v)",
					scope, want, got)
			}
		}
	}
	// Count must equal the sum of the bucket sizes — this is the wire
	// shape the CLI stamp / dashboard uses to render quota progress.
	wantTotal := len(respAll.SecretsByScope["default"]) +
		len(respAll.SecretsByScope["prod"]) +
		len(respAll.SecretsByScope["staging"])
	if respAll.Count != wantTotal {
		t.Errorf("scope=__all__: count=%d, want %d", respAll.Count, wantTotal)
	}

	// Assertion 11: quota counts across scopes — 9th PUT must 403
	// plan_limit_secrets because Free cap = 8. We already wrote
	// ALPHA@prod + ALPHA@staging + BETA@default = 3 rows; fill the
	// remaining five slots across named scopes before the boundary check.
	//
	for i := 0; i < limits.SecretCountMax-3; i++ {
		putAt("qa-prod", fmt.Sprintf("FILL_%d", i), "v-fill")
	}
	// The key name "GAMMA" is the new (9th) key — re-PUT of an existing
	// key would re-upsert (off-by-one rule) and NOT count against quota,
	// so a fresh key is what proves the cross-scope posture.
	//
	// The scope "qa-prod" (5 chars) is the minimal valid scope slug
	// beyond "default" — keeps the assertion on the cross-scope quota
	// path (the load-bearing assertion) rather than 400 env_scope_invalid
	// (which would fire for shorter slugs like "qa" that fail the
	// EnvScopePattern regex `^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`).
	assertProblemAPID(t, h, key, http.MethodPut,
		"/v1/apps/"+slug+"/secrets/GAMMA?scope=qa-prod",
		api.PutAppSecretRequest{Value: "v-qa-gamma"},
		http.StatusForbidden,
		api.CodePlanLimitSecrets)
}
