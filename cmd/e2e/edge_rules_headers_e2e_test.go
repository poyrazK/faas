// edge_rules_headers_e2e_test.go — D18 per-kind e2e for `kind=headers`.
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

func TestEdgeRulesHeaders_E2E(t *testing.T) {
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

	slug := "headers-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-headers.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=headers, response_headers=[{X-Test,ok,set}].
	// response-side ops are installed on `statusRecorder` BEFORE any
	// 4xx is written (handler.go:2289), so even a 404 from
	// Backend.Pick carries the X-Test header.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindHeaders,
		map[string]any{
			"kind": "headers",
			"headers": map[string]any{
				"response_headers": []map[string]any{
					{"name": "X-Test", "value": "ok", "action": "set"},
				},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: GET /. Backend.Pick misses → 404, but the X-Test
	// header is stamped on the response.
	header, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
	if status != http.StatusNotFound {
		t.Errorf("kind=headers happy: status=%d, want 404 (Backend.Pick miss; X-Test still stamped)", status)
	}
	if got := header.Get("X-Test"); got != "ok" {
		t.Errorf("kind=headers happy: X-Test=%q, want ok", got)
	}
}
