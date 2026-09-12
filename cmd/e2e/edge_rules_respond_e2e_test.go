//go:build !no_pg

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

func TestEdgeRulesRespond_E2E_ServesAndDisablesPreviewMock(t *testing.T) {
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

	// Preview apps are provisioned by githubd rather than the public API, so
	// mirror that dispatch path while keeping this test independent of GitHub.
	prodSlug := "respond-prod"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: prodSlug})
	var prod api.AppResponse
	if err := json.Unmarshal(createRec, &prod); err != nil {
		t.Fatalf("decode production app: %v body=%s", err, createRec)
	}
	previewSlug := "respond-preview"
	if err := insertPreviewApp(t, pool, accountID, previewSlug, prodSlug, 42, state.PreviewPrStateOpen); err != nil {
		t.Fatalf("insert preview: %v", err)
	}
	preview := getAppBySlug(t, pool, previewSlug)

	successHost := "edgectl-respond.apps.test.example"
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, preview.ID, successHost, previewSlug)
	successRuleID := seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, preview.ID, successHost,
		state.EdgeRuleKindRespond,
		map[string]any{
			"kind": "respond",
			"respond": map[string]any{
				"status_code": 200,
				"body":        map[string]any{"days": 3, "price": 4.99},
			},
		},
	)

	errorHost := "edgectl-respond-error.apps.test.example"
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, preview.ID, errorHost, previewSlug)
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, preview.ID, errorHost,
		state.EdgeRuleKindRespond,
		map[string]any{
			"kind": "respond",
			"respond": map[string]any{
				"status_code": 503,
				"body":        map[string]any{"error": "not ready"},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	headers, body, status := doReqHeaders(t, h, successHost,
		http.MethodGet, "/shipping/estimate", nil)
	if status != http.StatusOK {
		t.Fatalf("mock response status = %d, want 200; body=%s", status, body)
	}
	if got := headers.Get("Content-Type"); got != "application/json" {
		t.Errorf("mock Content-Type = %q, want application/json", got)
	}
	if got := string(body); got != `{"days":3,"price":4.99}` {
		t.Errorf("mock body = %q, want fixed JSON", got)
	}

	_, body, status = doReqHeaders(t, h, errorHost,
		http.MethodGet, "/shipping/estimate", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("error mock status = %d, want 503; body=%s", status, body)
	}
	if got := string(body); got != `{"error":"not ready"}` {
		t.Errorf("error mock body = %q, want fixed JSON", got)
	}

	// Toggle the rule off as the dashboard/CLI would, then reset the matcher
	// cache. The route substitute remains, so a 404 proves the fixed response
	// no longer short-circuits the normal backend path.
	if _, err := pool.Exec(context.Background(),
		`update edge_rules set enabled = false where id = $1`, successRuleID); err != nil {
		t.Fatalf("disable respond rule: %v", err)
	}
	resetEdgeRuleCache(t, h)
	_, body, status = doReqHeaders(t, h, successHost,
		http.MethodGet, "/shipping/estimate", nil)
	if status != http.StatusNotFound {
		t.Errorf("disabled mock status = %d, want 404 after backend fall-through; body=%s", status, body)
	}
}
