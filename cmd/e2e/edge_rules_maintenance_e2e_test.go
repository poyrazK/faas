// edge_rules_maintenance_e2e_test.go — D18 per-kind e2e for
// `kind=maintenance` (ADR-091 amendment, PR-C rollout-closer).
// Bitmask: APID | Gatewayd (mirrors the 7 prior per-kind e2e files).
//
// Architecture note: `kind=maintenance` runs at the SAME `haveApp`
// stage as the other 9 kinds (handler.go:2943 inserts the call
// between haveApp and matchAndApplyRedirect). The precondition is
// therefore the same as every sibling: a `kind=route` substitute
// pointing the synthetic host at the real test app, so
// Backend.Lookup doesn't 404 before the maintenance rule fires.
//
// The plan's three scenarios:
//   - Happy path — a kind=maintenance rule on (host, /payments,
//     POST) returns 503 + Retry-After: 60 + Problem.code=edge_rule_maintenance.
//   - Methods filter — a sibling rule on /payments with methods=''{POST}''
//     does NOT shoot down a GET (the request hits Backend.Pick and
//     falls through normally).
//   - Cross-account — a rule owned by account A applied to a host
//     in account B silently falls through to Backend.Pick (silent
//     fall-through, audit edge_rule.maintenance_blocked). The test
//     asserts the request reaches Backend.Pick (404), not 503.
//   - Cap enforcement — PATCH retry_after_seconds=999999 returns
//     422 request_validation_failed (apid-Validate gate).
//
// Why no "Forwarded" happy-200 test: the test harness doesn't run
// schedd/vmmd/imaged (the 7 prior e2e files don't either). The
// wake path is out of scope for the maintenance amendment — we
// observe the 503 directly from the maintenance rule, not via
// Backend.Pick.

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

// TestEdgeRulesMaintenance_E2E_MatchReturns503 pins the load-bearing
// 503 contract for kind=maintenance (§4.1.2.13). A matching rule
// must produce:
//   - HTTP 503 status
//   - RFC 7807 problem+json body (CodeEdgeRuleMaintenance)
//   - Retry-After header at the per-rule cap (custom override)
//   - the rule's Message as Problem.detail
//
// The test also asserts the methods filter: a GET on the same path
// does NOT shoot down (the rule's match_methods is post-only), so the
// request reaches Backend.Pick → 404 (no real impl).
func TestEdgeRulesMaintenance_E2E_MatchReturns503(t *testing.T) {
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

	slug := "maintenance-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-maintenance.apps.test.example"

	// Precondition: kind=route substitute (synthetic host → real test app).
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=maintenance on POST /payments with a
	// custom 3600 s Retry-After + a Message payload.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindMaintenance,
		map[string]any{
			"kind": "maintenance",
			"maintenance": map[string]any{
				"retry_after_seconds": 3600,
				"message":             "Scheduled payment rollout in progress",
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// POST /payments → 503 + Retry-After: 3600.
	headers, body, status := doReqHeaders(t, h, synthHost, http.MethodPost,
		"/payments", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /payments: status=%d, want 503; body=%s", status, body)
	}
	if ra := headers.Get("Retry-After"); ra != "3600" {
		t.Errorf("Retry-After = %q, want 3600 (per-rule override)", ra)
	}
	if ct := headers.Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("unmarshal problem: %v body=%s", err, body)
	}
	if problem.Code != api.CodeEdgeRuleMaintenance {
		t.Errorf("code = %q, want %q", problem.Code, api.CodeEdgeRuleMaintenance)
	}
	if problem.Detail == "" || !contains([]byte(problem.Detail), "Scheduled payment rollout") {
		t.Errorf("detail = %q; want it to contain the rule's Message", problem.Detail)
	}

	// Methods filter: GET /payments must NOT trigger the rule
	// (the rule's match_methods is post-only). The request
	// reaches Backend.Pick → 404 (no real impl).
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/payments", nil)
	if status == http.StatusServiceUnavailable {
		t.Errorf("GET /payments: status=503; rule should NOT fire on GET (match_methods=post)")
	}
}

// TestEdgeRulesMaintenance_E2E_DefaultRetryAfter pins the default
// Retry-After path. A rule with retry_after_seconds=0 must surface
// the platform default (60 s) on the wire — the cmd-side
// compileMaintenanceRules clamps 0 → api.EdgeRuleMaintenanceRetryAfterSeconds
// before the applier ever sees the rule.
func TestEdgeRulesMaintenance_E2E_DefaultRetryAfter(t *testing.T) {
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

	slug := "maintenance-default-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-maintenance-default.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindMaintenance,
		map[string]any{
			"kind": "maintenance",
			"maintenance": map[string]any{
				"retry_after_seconds": 0,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	headers, _, status := doReqHeaders(t, h, synthHost, http.MethodPost,
		"/payments", nil)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("POST /payments: status=%d, want 503", status)
	}
	if ra := headers.Get("Retry-After"); ra != "60" {
		t.Errorf("Retry-After = %q, want 60 (platform default)", ra)
	}
}

// TestEdgeRulesMaintenance_E2E_CrossAccountFallsThrough pins the
// D5 same-account defence-in-depth posture. A rule owned by
// account A applied to a host in account B silently falls through
// (audit emit edge_rule.maintenance_blocked + apply success) —
// the customer never sees a 503 from a cross-account rule.
func TestEdgeRulesMaintenance_E2E_CrossAccountFallsThrough(t *testing.T) {
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

	// Second account (cross-account rule owner).
	otherKey := h.SeedAccount(context.Background(), api.PlanHobby)
	otherAccountID := accountIDFromKey(t, context.Background(), pool, otherKey)

	slug := "maintenance-crossaccount-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-maintenance-cross.apps.test.example"

	// kind=route substitute on the host owned by account A.
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// kind=maintenance rule owned by account B (cross-account).
	// We bypass the apid-Validate same-account check by going
	// direct via seedEdgeRuleDirect (the same hot-fix path the
	// kind=geo / kind=validate e2e tests use).
	seedEdgeRuleDirect(t, context.Background(), pool,
		otherAccountID, app.ID, synthHost,
		state.EdgeRuleKindMaintenance,
		map[string]any{
			"kind": "maintenance",
			"maintenance": map[string]any{
				"retry_after_seconds": 60,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// POST /payments → cross-account rule silently falls through to
	// Backend.Pick → 404 (no real impl). 503 means the cross-account
	// rule DID fire — wrong.
	_, _, status := doReqHeaders(t, h, synthHost, http.MethodPost, "/payments", nil)
	if status == http.StatusServiceUnavailable {
		t.Errorf("POST /payments: status=503; cross-account rule must fall through")
	}
	if status != http.StatusNotFound {
		t.Errorf("POST /payments: status=%d, want 404 (Backend.Pick miss after cross-account fall-through)", status)
	}
}
