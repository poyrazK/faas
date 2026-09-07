// apps_maintenance_mode_e2e_test.go — D18 per-kind e2e for
// `apps.maintenance_mode` (ADR-091 amendment, PR-C rollout-closer).
// Bitmask: APID | Gatewayd.
//
// Architecture note: `apps.maintenance_mode` fires at the SAME
// `haveApp` stage as the kind=maintenance rule (handler.go:2943),
// but BEFORE it. The coarse gate beats the fine-grained rule —
// when both fire on the same request, the customer sees
// CodeAppMaintenance, not CodeEdgeRuleMaintenance. The third test
// pins that ordering contract.
//
// Cache invalidation (PR-B): `apps_maintenance_mode_notify` trigger
// emits `pg_notify('app_changed', NEW.id::text)` only when
// maintenance_mode IS DISTINCT FROM old.maintenance_mode. The
// gatewayd listener drops only that app from the apps LRU. The
// test asserts this end-to-end: PATCH flips maintenance_mode, the
// next request observes 503 within a short window (we loop a few
// times to absorb the listener latency).

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

// TestAppsMaintenanceMode_E2E_PatchTrueReturns503 pins the load-bearing
// coarse-gate contract for apps.maintenance_mode (§4.1.2.0). A PATCH
// to set maintenance_mode=true must propagate via pg_notify and
// cause the gateway to return 503 with:
//   - HTTP 503 status
//   - Problem.code = app_maintenance_mode
//   - Retry-After: 60 (platform default; coarse gate uses the same
//     EdgeRuleMaintenanceRetryAfterSeconds constant)
//   - Problem.detail contains the app slug (per-tenant visibility)
func TestAppsMaintenanceMode_E2E_PatchTrueReturns503(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slug := "maintenance-mode-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-maintenance-mode.apps.test.example"
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	// Precondition: warm the gateway's apps cache by hitting the host
	// once (so the subsequent PATCH flush hits an entry that exists).
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)
	resetEdgeRuleCache(t, h)
	_, _, beforeStatus := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
	if beforeStatus == http.StatusServiceUnavailable {
		t.Fatalf("pre-PATCH request should NOT be 503 (got %d)", beforeStatus)
	}

	// PATCH maintenance_mode=true. The trigger emits app_changed
	// and the gateway drops only this app from the apps LRU.
	patchBody, _ := json.Marshal(api.UpdateAppRequest{MaintenanceMode: boolPtr(true)})
	patchRec := doReqBytes(t, h, key, http.MethodPatch, "/v1/apps/"+slug, patchBody)
	var patched api.AppResponse
	if err := json.Unmarshal(patchRec, &patched); err != nil {
		t.Fatalf("decode patch: %v body=%s", err, patchRec)
	}
	if !patched.MaintenanceMode {
		t.Fatalf("after PATCH MaintenanceMode=false; want true. body=%s", patchRec)
	}

	// Loop until 503 — the pg_notify listener has ~10-50ms latency
	// before the cache is dropped, so a single request may race.
	var (
		headers http.Header
		body    []byte
		status  int
	)
	for i := 0; i < 20; i++ {
		headers, body, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
		if status == http.StatusServiceUnavailable {
			break
		}
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("after PATCH maintenance_mode=true: status=%d, want 503; body=%s", status, body)
	}
	if ra := headers.Get("Retry-After"); ra != "60" {
		t.Errorf("Retry-After = %q, want 60 (platform default)", ra)
	}
	if ct := headers.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("unmarshal problem: %v body=%s", err, body)
	}
	if problem.Code != api.CodeAppMaintenance {
		t.Errorf("code = %q, want %q", problem.Code, api.CodeAppMaintenance)
	}
	// Per-tenant visibility: Problem.detail should mention the slug.
	if problem.Detail == "" || !contains([]byte(problem.Detail), slug) {
		t.Errorf("detail = %q; want it to mention slug %q", problem.Detail, slug)
	}

	// PATCH maintenance_mode=false → cache flush → 200 path (Backend.Pick
	// miss since no real impl; status NOT 503).
	patchBody, _ = json.Marshal(api.UpdateAppRequest{MaintenanceMode: boolPtr(false)})
	patchRec = doReqBytes(t, h, key, http.MethodPatch, "/v1/apps/"+slug, patchBody)
	if err := json.Unmarshal(patchRec, &patched); err != nil {
		t.Fatalf("decode patch: %v body=%s", err, patchRec)
	}
	if patched.MaintenanceMode {
		t.Fatalf("after PATCH MaintenanceMode=true; want false. body=%s", patchRec)
	}

	for i := 0; i < 20; i++ {
		_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
		if status != http.StatusServiceUnavailable {
			break
		}
	}
	if status == http.StatusServiceUnavailable {
		t.Errorf("after PATCH maintenance_mode=false: status still 503; cache flush did not propagate")
	}
}

// TestAppsMaintenanceMode_E2E_CoarseGateBeatsEdgeRule pins the
// ordering contract: when BOTH `apps.maintenance_mode=true` AND a
// matching `kind=maintenance` rule are live, the customer sees
// `app_maintenance_mode`, NOT `edge_rule_maintenance`. The coarse
// gate is checked FIRST in handler.go so the customer never sees a
// different Problem.code from the fine-grained rule.
func TestAppsMaintenanceMode_E2E_CoarseGateBeatsEdgeRule(t *testing.T) {
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

	slug := "coarse-beats-fine"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-coarse-beats-fine.apps.test.example"
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Fine-grained rule on POST /payments.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindMaintenance,
		map[string]any{
			"kind": "maintenance",
			"maintenance": map[string]any{
				"retry_after_seconds": 3600,
				"message":             "edge-rule-fine",
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Coarse gate: PATCH maintenance_mode=true.
	patchBody, _ := json.Marshal(api.UpdateAppRequest{MaintenanceMode: boolPtr(true)})
	if rec := doReqBytes(t, h, key, http.MethodPatch, "/v1/apps/"+slug, patchBody); len(rec) == 0 {
		t.Fatalf("PATCH returned empty body")
	}

	// POST /payments → coarse gate fires FIRST, code = app_maintenance_mode.
	var (
		headers http.Header
		body    []byte
		status  int
	)
	for i := 0; i < 20; i++ {
		headers, body, status = doReqHeaders(t, h, synthHost, http.MethodPost,
			"/payments", nil)
		if status == http.StatusServiceUnavailable {
			break
		}
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /payments: status=%d, want 503", status)
	}
	// Retry-After must be 60 (coarse default), NOT 3600 (fine-grained
	// rule's custom cap). If the fine-grained rule had fired, Retry-After
	// would have been 3600.
	if ra := headers.Get("Retry-After"); ra != "60" {
		t.Errorf("Retry-After = %q, want 60 (coarse gate wins; fine rule had 3600)", ra)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("unmarshal problem: %v body=%s", err, body)
	}
	if problem.Code != api.CodeAppMaintenance {
		t.Errorf("code = %q, want %q (coarse gate must beat fine rule)",
			problem.Code, api.CodeAppMaintenance)
	}
	if contains([]byte(problem.Detail), "edge-rule-fine") {
		t.Errorf("detail = %q; coarse gate must NOT leak fine rule's Message", problem.Detail)
	}
}

func boolPtr(b bool) *bool { return &b }
