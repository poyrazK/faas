// edge_rules_throttle_e2e_test.go — D20.5 amendment (issue #881)
// per-kind e2e for `kind=throttle`.
//
// Bitmask: APID | Gatewayd. See edge_rules_common_test.go for the
// kind=route-substitute precondition pattern (synthetic host → real
// test app slug) that every per-kind test seeds to get past
// Backend.Lookup.
//
// The throttle returns 429 with `x-faas-rate-limit-scope: route` +
// `X-RouteRateLimit-{Limit,Remaining,Reset}` once the bucket is
// exhausted. The e2e pin is the contract surface: 429 status, scope
// header, Retry-After, and the RFC 7807 Problem.code. The exact
// per-second timing is deterministic in the gateway because the
// `*Limiter.AllowWithParams` is a pure function of (rps, burst, now)
// — driving N requests back-to-back exhausts the bucket cleanly
// in the test harness.

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

func TestEdgeRulesThrottle_E2E_429Contract(t *testing.T) {
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

	slug := "throttle-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-throttle.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=throttle, rps=1, burst=2. The bucket
	// starts FULL at burst=2; the first 2 requests pass, the 3rd
	// is denied as 429. The harness drives them back-to-back so
	// the refill formula (rps * dt) doesn't refill any tokens
	// between requests.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindThrottle,
		map[string]any{
			"kind": "throttle",
			"throttle": map[string]any{
				"requests_per_second": 1.0,
				"burst":               2,
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// First 2 requests: bucket is full, throttle passes. The
	// downstream Backend.Pick has no real impl so the gateway
	// returns 404 — but the throttle itself did NOT fire.
	for i := 0; i < 2; i++ {
		headers, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
		if status != http.StatusNotFound {
			t.Errorf("req %d: status=%d, want 404 (Backend.Pick miss after throttle pass)", i, status)
		}
		// The throttle writes X-RouteRateLimit-* even on the
		// pass path (mirror of the per-app rate-limit headers).
		// The 429 contract is what the test pins; the pass-path
		// headers are in the unit tests.
		_ = headers
	}

	// 3rd request: bucket is exhausted, throttle fires 429.
	headers, body, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
	if status != http.StatusTooManyRequests {
		t.Errorf("3rd req: status=%d, want 429 (bucket exhausted); body=%s", status, body)
	}
	// The load-bearing contract assertions: the throttle MUST
	// emit the route-scoped scope header (distinguishing it from
	// the per-app + per-account rate-limits) and the RFC 7807
	// Problem envelope with a stable code.
	if got := headers.Get("x-faas-rate-limit-scope"); got != "route" {
		t.Errorf("x-faas-rate-limit-scope = %q, want %q", got, "route")
	}
	// ADR-104 amendment 5 (issue #881 Phase 4 H1): the throttle
	// 429 path MUST emit X-RouteRateLimit-Policy. For a
	// back-compat rule (KeyBy="" — the default for the seedEdgeRuleDirect
	// action shape above) the value is the literal "route". The
	// per-consumer collapse path is pinned by the pkg/gateway unit
	// tests (TestEdgeRuleThrottlePolicyHeader_PerConsumerCollapse);
	// e2e is intentionally limited to the contract surface the
	// gatewayd process emits without an authn chain.
	if got := headers.Get("X-RouteRateLimit-Policy"); got != "route" {
		t.Errorf("X-RouteRateLimit-Policy = %q, want %q (back-compat default for KeyBy=\"\" rules)", got, "route")
	}
	if got := headers.Get("Retry-After"); got == "" {
		t.Errorf("Retry-After header missing on 429")
	}
	// Body shape: RFC 7807 application/problem+json, code = rate_limited.
	var problem api.Problem
	if err := json.Unmarshal(body, &problem); err != nil {
		t.Fatalf("decode 429 body: %v body=%s", err, body)
	}
	if problem.Code != "rate_limited" {
		t.Errorf("Problem.code = %q, want %q", problem.Code, "rate_limited")
	}
}
