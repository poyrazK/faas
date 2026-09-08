// edge_rules_cors_e2e_test.go — D18 per-kind e2e for `kind=cors`.
// Bitmask: APID | Gatewayd. See edge_rules_common_test.go for the
// kind=route-substitute precondition pattern.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEdgeRulesCORS_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "cors-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-cors.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=cors, AllowOrigins=[https://app.test],
	// AllowMethods=[POST,GET], AllowCredentials=true. The Origin
	// echo + Allow-Methods header are the assertion surface.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindCORSA,
		map[string]any{
			"kind": "cors",
			"cors": map[string]any{
				"allow_origins":     []string{"https://app.test"},
				"allow_methods":     []string{"POST", "GET"},
				"allow_credentials": true,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: OPTIONS preflight. CORS short-circuits with 204
	// BEFORE redirect (handler.go:2296). Browser preflight must NOT
	// get a 3xx.
	header, _, status := doReqHeaders(t, h, synthHost, http.MethodOptions,
		"/", nil, map[string]string{
			"Origin":                         "https://app.test",
			"Access-Control-Request-Method":  "POST",
			"Access-Control-Request-Headers": "X-Requested-With",
		})
	if status != http.StatusNoContent {
		t.Errorf("kind=cors preflight: status=%d, want 204", status)
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "https://app.test" {
		t.Errorf("kind=cors preflight: ACAO=%q, want https://app.test", got)
	}
	if got := header.Get("Access-Control-Allow-Methods"); got != "" && !stringContains(got, "POST") {
		t.Errorf("kind=cors preflight: ACAM=%q, want to contain POST", got)
	}
	if got := header.Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("kind=cors preflight: ACAC=%q, want true", got)
	}

	// Negative path: a host with NO kind=cors rule but with the
	// kind=route substitute, hitting OPTIONS preflight without a
	// matching CORS rule falls through to Backend.Pick → 404 (no
	// routable target). This is distinct from a 204 with no CORS
	// headers — gateway's CORS hook returns false when no rule
	// matches.
	header, _, status = doReqHeaders(t, h, "edgectl-nocors.apps.test.example",
		http.MethodOptions, "/", nil, map[string]string{
			"Origin":                        "https://app.test",
			"Access-Control-Request-Method": "POST",
		})
	if status == http.StatusNoContent {
		t.Errorf("kind=cors negative: status=%d, want NOT 204 (no CORS rule → no short-circuit)", status)
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("kind=cors negative: ACAO=%q, want empty (no CORS rule)", got)
	}
}

// TestEdgeRulesCORS_NonPreflight_HappyPath is the ADR-091 D20.6
// twin of the preflight test above. The preflight path is covered
// (it short-circuits at the CORS hook with a 204); the non-preflight
// path was unit-level but had no e2e — operators couldn't tell
// whether the GET-fall-through-with-ACAO path was wired correctly
// in production. PR-B closes D20.6.
//
// Walks the same rule shape (kind=cors, allow_origins=[https://app.test],
// allow_methods=[POST, GET]) but issues a GET instead of OPTIONS.
// The CORS hook must fall through (NOT 204) and the proxied response
// must carry Access-Control-Allow-Origin: https://app.test. The
// test asserts both halves of the contract:
//
//   - Status: NOT 204 (no short-circuit on non-preflight).
//   - ACAO header: stamped on the proxied response via the
//     statusRecorder installHeaderOps path.
//
// Bitmask: APID | Gatewayd (same as the preflight test).
func TestEdgeRulesCORS_NonPreflight_HappyPath(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "cors-nonpreflight-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-cors-np.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Same kind=cors rule shape as the preflight test so this PR's
	// non-preflight assertion is directly comparable: identical
	// allow_origins / allow_methods / allow_credentials.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindCORSA,
		map[string]any{
			"kind": "cors",
			"cors": map[string]any{
				"allow_origins":     []string{"https://app.test"},
				"allow_methods":     []string{"POST", "GET"},
				"allow_credentials": true,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy-path non-preflight: GET / with Origin: https://app.test.
	// The CORS hook MUST fall through (NOT 204) and MUST stamp the
	// Access-Control-Allow-Origin header on the proxied response.
	header, _, status := doReqHeaders(t, h, synthHost, http.MethodGet,
		"/", nil, map[string]string{
			"Origin": "https://app.test",
		})
	if status == http.StatusNoContent {
		t.Errorf("kind=cors non-preflight: status=204 (preflight short-circuit on a GET); " +
			"the CORS hook must fall through for non-OPTIONS")
	}
	if got := header.Get("Access-Control-Allow-Origin"); got != "https://app.test" {
		t.Errorf("kind=cors non-preflight: ACAO=%q, want %q",
			got, "https://app.test")
	}
}

func stringContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
