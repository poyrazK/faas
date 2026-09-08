// edge_rules_redirect_e2e_test.go — D18 per-kind e2e for `kind=redirect`.
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

func TestEdgeRulesRedirect_E2E(t *testing.T) {
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

	slug := "redirect-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-redirect.apps.test.example"

	// Precondition: kind=route substitute.
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=redirect, status=308, To="https://elsewhere.example/path".
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindRedirect,
		map[string]any{
			"kind": "redirect",
			"redirect": map[string]any{
				"status_code": 308,
				"to":          "https://elsewhere.example/path",
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: GET / on the synthetic host. kind=redirect
	// short-circuits with 308 + Location header BEFORE Backend.Lookup
	// (handler.go:2284-2287). Assert status=308 AND
	// Location=https://elsewhere.example/path.
	header, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
	if status != http.StatusPermanentRedirect {
		t.Errorf("kind=redirect happy: status=%d, want 308", status)
	}
	if got := header.Get("Location"); got != "https://elsewhere.example/path" {
		t.Errorf("kind=redirect happy: Location=%q, want https://elsewhere.example/path", got)
	}

	// Negative path: a host with NO seeded kind=redirect rule. We
	// reuse a different synthHost without any rules — fall through
	// Backend.Lookup miss → 404. (No rule substitute here, so we
	// expect the bare 404 from handler.go:2261.)
	_, _, status = doReqHeaders(t, h, "edgectl-norule.apps.test.example",
		http.MethodGet, "/", nil)
	if status != http.StatusNotFound {
		t.Errorf("kind=redirect negative: status=%d, want 404 (no rule, no app)", status)
	}
}
