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
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)})
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
		headers, body, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil)
		assertBackendFallthrough(t, status, body)
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
	// dimensional value is exercised by the consumer-isolation e2e below.
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

func TestEdgeRulesThrottle_E2E_ConsumerDimensionsAreIndependent(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID|e2etest.Gatewayd, nil)
	accountKey := h.SeedAccount(context.Background(), api.PlanHobby)
	accountID := accountIDFromKey(t, context.Background(), pool, accountKey)
	slug := "throttle-consumer-dimensions"
	rawApp, appStatus := doReq(t, h, accountKey, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug, RequireAuthn: boolPtr(false)})
	if appStatus != http.StatusCreated {
		t.Fatalf("create app: status=%d body=%s", appStatus, rawApp)
	}
	var app api.AppResponse
	if err := json.Unmarshal(rawApp, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, rawApp)
	}

	createConsumerKey := func(externalRef string) string {
		t.Helper()
		rawConsumer, status := doReq(t, h, accountKey, http.MethodPost,
			"/v1/apps/"+slug+"/consumers", api.CreateAPIConsumerRequest{
				ExternalRef: externalRef,
				Name:        externalRef,
			})
		if status != http.StatusCreated {
			t.Fatalf("create consumer %s: status=%d body=%s", externalRef, status, rawConsumer)
		}
		var consumer api.APIConsumerResponse
		if err := json.Unmarshal(rawConsumer, &consumer); err != nil {
			t.Fatalf("decode consumer %s: %v body=%s", externalRef, err, rawConsumer)
		}
		rawKey, status := doReq(t, h, accountKey, http.MethodPost,
			"/v1/apps/"+slug+"/consumers/"+consumer.ID+"/keys",
			api.CreateConsumerKeyRequest{Name: externalRef + "-primary", Scopes: []string{"read"}})
		if status != http.StatusCreated {
			t.Fatalf("create key for %s: status=%d body=%s", externalRef, status, rawKey)
		}
		var key api.ConsumerKeyResponse
		if err := json.Unmarshal(rawKey, &key); err != nil {
			t.Fatalf("decode key for %s: %v body=%s", externalRef, err, rawKey)
		}
		if key.Key == "" {
			t.Fatalf("create key for %s returned no plaintext", externalRef)
		}
		return key.Key
	}
	consumerAKey := createConsumerKey("customer-a")
	consumerBKey := createConsumerKey("customer-b")

	synthHost := "edgectl-throttle-consumers.apps.test.example"
	seedRouteSubstitute(t, context.Background(), pool, accountID, app.ID, synthHost, slug)
	action, err := json.Marshal(api.EdgeRuleThrottleAction{
		RequestsPerSecond: 0.01,
		Burst:             1,
		KeyBy:             api.ThrottleKeyByConsumerID,
		MaxKeysPerRule:    100,
		MissingKeyPolicy:  api.ThrottleMissingKeyReject,
	})
	if err != nil {
		t.Fatalf("marshal throttle action: %v", err)
	}
	rawRule, ruleStatus := doReq(t, h, accountKey, http.MethodPost,
		"/v1/apps/"+slug+"/edge-rules", api.CreateEdgeRuleRequest{
			MatchHost: synthHost,
			MatchPath: "/*",
			Kind:      string(state.EdgeRuleKindThrottle),
			Action:    action,
		})
	if ruleStatus != http.StatusCreated {
		t.Fatalf("create dimensional throttle: status=%d body=%s", ruleStatus, rawRule)
	}
	resetEdgeRuleCache(t, h)

	_, anonymousBody, anonymousStatus := gatewayReq(t, h, http.MethodGet, "/", nil,
		gatewayReqOptions{Host: synthHost})
	if anonymousStatus != http.StatusUnauthorized || problemCode(anonymousBody) != api.CodeUnauthorized {
		t.Fatalf("anonymous request = status %d code %q, want 401/%q; body=%s",
			anonymousStatus, problemCode(anonymousBody), api.CodeUnauthorized, anonymousBody)
	}

	requestAs := func(key string) (http.Header, []byte, int) {
		t.Helper()
		return gatewayReq(t, h, http.MethodGet, "/", nil, gatewayReqOptions{
			Host: synthHost, Authorization: "Bearer " + key,
		})
	}
	_, body, status := requestAs(consumerAKey)
	assertBackendFallthrough(t, status, body)
	headers, body, status := requestAs(consumerAKey)
	if status != http.StatusTooManyRequests {
		t.Fatalf("consumer A second request: status=%d, want 429; body=%s", status, body)
	}
	if got := headers.Get("X-RouteRateLimit-Policy"); got != "per-consumer" {
		t.Errorf("consumer A policy=%q, want per-consumer", got)
	}
	if got := headers.Get("X-RouteRateLimit-Remaining"); got != "0" {
		t.Errorf("consumer A remaining=%q, want 0", got)
	}

	// Consumer B must receive its own full burst even though consumer A has
	// exhausted theirs. A shared parent route bucket would reject this request.
	_, body, status = requestAs(consumerBKey)
	assertBackendFallthrough(t, status, body)
	_, body, status = requestAs(consumerBKey)
	if status != http.StatusTooManyRequests {
		t.Fatalf("consumer B second request: status=%d, want 429; body=%s", status, body)
	}
}
