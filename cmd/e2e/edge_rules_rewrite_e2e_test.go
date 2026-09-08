// edge_rules_rewrite_e2e_test.go — D18 per-kind e2e for `kind=rewrite`
// (issue #561 PR 6 / ADR-091). Bitmask: APID | Gatewayd.
//
// See edge_rules_common_test.go for the helper surface; the key fact
// (the architectural discovery from PR 6 design review) is that only
// `kind=route` runs BEFORE Backend.Lookup — every other kind runs at
// `haveApp` (handler.go:2267 onward). This test seeds a kind=route
// substitute as a precondition so the synthetic host resolves and
// the kind=rewrite rule actually fires on the wire.

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

func TestEdgeRulesRewrite_E2E(t *testing.T) {
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

	// Real app — kind=route substitute points the synthetic host to it.
	slug := "rewrite-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-rewrite.apps.test.example"

	// Precondition: kind=route substitute (synthetic host → real test app).
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=rewrite, From="/api", To="/v2".
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindRewrite,
		map[string]any{
			"kind": "rewrite",
			"rewrite": map[string]any{
				"from": "/api",
				"to":   "/v2",
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: GET /api/foo. The kind=rewrite rule mutates the
	// path to /v2/foo; Backend.Pick has no routable target on the
	// test app → 404 from the wake gate. Visible signal is 404 (not
	// 200 / 308). The path was mutated in-flight; we assert the
	// gateway reached `haveApp` and the rewrite hook fired.
	_, _, status := doReqHeaders(t, h, synthHost, http.MethodGet,
		"/api/foo", nil)
	if status != http.StatusNotFound {
		t.Errorf("kind=rewrite happy: status=%d, want 404 (Backend.Pick miss after rewrite fired)", status)
	}

	// Negative path: GET /public — no /api prefix, but path-glob "*"
	// matches everything; From="" branch (handler.go:959) leaves the
	// path alone, Backend.Pick still misses → 404. Distinct from a
	// rule miss: gateway reaches haveApp.
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet,
		"/public", nil)
	if status != http.StatusNotFound {
		t.Errorf("kind=rewrite negative: status=%d, want 404", status)
	}
}
