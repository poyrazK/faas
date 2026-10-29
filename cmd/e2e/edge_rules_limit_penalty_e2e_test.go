// edge_rules_limit_penalty_e2e_test.go — per-kind e2e for `kind=limit`
// (ADR-091 D24) and the penalty box (ADR-20261022105846), the two refusal
// paths the edge-rule suite did not cover end to end (ADR-091 amendment
// 2026-10-27, GA hardening). Bitmask: APID | Gatewayd. Rules are created
// through the API, so apid validation, the convergence barrier and the
// gateway's rule load all run; see edge_rules_common_test.go for the
// kind=route-substitute precondition.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// createEdgeRuleE2E creates a rule through the API and returns its ID.
func createEdgeRuleE2E(t *testing.T, h *e2etest.Harness, key, slug string, req map[string]any) string {
	t.Helper()
	body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req)
	if status != http.StatusCreated {
		t.Fatalf("create %v rule: status=%d body=%s", req["kind"], status, body)
	}
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(body, &rule); err != nil {
		t.Fatalf("decode rule: %v body=%s", err, body)
	}
	return rule.ID
}

// TestEdgeRulesLimit_E2E: a body over the route's cap is refused with 413
// before the app is reached; a smaller body, another path and another method
// pass; and the API refuses a cap of zero or above 25 MiB.
func TestEdgeRulesLimit_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(ctx, api.PlanHobby)
	accountID := accountIDFromKey(t, ctx, pool, key)
	slug := "limit-test-app"
	var app api.AppResponse
	if err := json.Unmarshal(doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)}), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	host := "edgectl-limit.edge-e2e.example"
	seedRouteSubstitute(t, ctx, pool, accountID, app.ID, host, slug)

	for _, maxBytes := range []int{0, api.MaxRequestBodyBytes + 1} {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", map[string]any{
			"kind": "limit", "match_host": host, "match_path": "/upload",
			"action": map[string]any{"max_body_bytes": maxBytes},
		})
		if status != http.StatusUnprocessableEntity {
			t.Fatalf("max_body_bytes=%d: status=%d body=%s, want 422", maxBytes, status, body)
		}
	}
	createEdgeRuleE2E(t, h, key, slug, map[string]any{
		"kind": "limit", "match_host": host, "match_path": "/upload", "match_methods": []string{"POST"},
		"action": map[string]any{"max_body_bytes": 1024},
	})
	resetEdgeRuleCache(t, h)

	big := map[string]string{"data": strings.Repeat("x", 2048)}
	_, body, status := doReqHeaders(t, h, host, http.MethodPost, "/upload", big)
	if status != http.StatusRequestEntityTooLarge || problemCode(body) != api.CodeRequestTooLarge {
		t.Fatalf("oversize POST /upload: status=%d code=%q body=%s, want 413 %s", status, problemCode(body), body, api.CodeRequestTooLarge)
	}
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil || problem.Limit == nil || *problem.Limit != 1024 {
		t.Fatalf("oversize problem limit = %+v (err %v), want 1024", problem.Limit, err)
	}

	_, body, status = doReqHeaders(t, h, host, http.MethodPost, "/upload", map[string]string{"data": "small"})
	assertBackendFallthrough(t, status, body)
	_, body, status = doReqHeaders(t, h, host, http.MethodPost, "/other", big)
	assertBackendFallthrough(t, status, body)
	_, body, status = doReqHeaders(t, h, host, http.MethodPut, "/upload", big)
	assertBackendFallthrough(t, status, body)
}

// TestEdgeRulesPenaltyBox_E2E: once a rule with a penalty has refused a
// client the configured number of times, every request from that client to
// the app is refused with 403 penalty_box and Retry-After, on any path;
// other clients are unaffected; clearing the penalty releases the client
// while the rule itself keeps refusing.
func TestEdgeRulesPenaltyBox_E2E(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	key := h.SeedAccount(ctx, api.PlanHobby)
	accountID := accountIDFromKey(t, ctx, pool, key)
	slug := "penalty-test-app"
	var app api.AppResponse
	if err := json.Unmarshal(doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)}), &app); err != nil {
		t.Fatalf("decode app: %v", err)
	}
	host := "edgectl-penalty.edge-e2e.example"
	seedRouteSubstitute(t, ctx, pool, accountID, app.ID, host, slug)
	releaseEdgeFeatures(t, pool)

	ruleID := createEdgeRuleE2E(t, h, key, slug, map[string]any{
		"kind": "ip", "match_host": host, "match_path": "/admin/**",
		"action":  map[string]any{"deny": []string{"192.0.2.0/24"}},
		"penalty": map[string]any{"seconds": 600, "after": 2, "within_seconds": 60},
	})
	resetEdgeRuleCache(t, h)

	from := func(ip string) map[string]string { return map[string]string{"X-Forwarded-For": ip} }
	boxed, other := from("192.0.2.7"), from("198.51.100.4")
	for _, path := range []string{"/admin/a", "/admin/b"} {
		_, body, status := doReqHeaders(t, h, host, http.MethodGet, path, nil, boxed)
		if status != http.StatusForbidden || problemCode(body) != api.CodeIPDenied {
			t.Fatalf("GET %s from a denied client: status=%d code=%q, want 403 %s", path, status, problemCode(body), api.CodeIPDenied)
		}
	}
	hdr, body, status := doReqHeaders(t, h, host, http.MethodGet, "/public", nil, boxed)
	if status != http.StatusForbidden || problemCode(body) != api.CodePenaltyBox {
		t.Fatalf("GET /public from a boxed client: status=%d code=%q body=%s, want 403 %s", status, problemCode(body), body, api.CodePenaltyBox)
	}
	if secs, err := strconv.Atoi(hdr.Get("Retry-After")); err != nil || secs <= 0 || secs > 600 {
		t.Fatalf("Retry-After = %q, want 1..600 seconds", hdr.Get("Retry-After"))
	}
	for _, path := range []string{"/public", "/admin/a"} {
		_, body, status = doReqHeaders(t, h, host, http.MethodGet, path, nil, other)
		assertBackendFallthrough(t, status, body)
	}

	body, status = doReq(t, h, key, http.MethodPatch, "/v1/edge-rules/"+ruleID, map[string]any{"clear_penalty": true})
	if status != http.StatusOK {
		t.Fatalf("clear penalty: status=%d body=%s", status, body)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_, body, status = doReqHeaders(t, h, host, http.MethodGet, "/public", nil, boxed)
		if problemCode(body) != api.CodePenaltyBox {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("clearing the rule's penalty did not release the boxed client within 10s")
		}
		time.Sleep(200 * time.Millisecond)
	}
	assertBackendFallthrough(t, status, body)
	_, body, status = doReqHeaders(t, h, host, http.MethodGet, "/admin/c", nil, boxed)
	if status != http.StatusForbidden || problemCode(body) != api.CodeIPDenied {
		t.Fatalf("GET /admin/c after release: status=%d code=%q, want the rule's own 403 %s", status, problemCode(body), api.CodeIPDenied)
	}
}
