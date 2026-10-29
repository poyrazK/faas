// edge_rules_api_e2e_test.go — edge rules end to end through the API, on
// every pull request (e2e-general, suite edge-rules). The per-kind tests in
// this directory seed rules straight into Postgres and run nightly; these
// create, change, roll back and delete rules through apid, so each change
// also crosses the generation barrier to the real gateways before it is
// checked on public traffic (ADR-091; amendment 2026-10-28). They are the
// acceptance test of the edge-rules capability.

package e2e_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newEdgeRulesFixture boots the normal-path stack with one live Hobby app.
// Hobby includes every kind used here (jwt and ip are paid-only) and leaves
// the app's public routes open. The gateway is a named serving node, so each
// rule change waits for its acknowledgement before the API answers; without
// one, apid has no gateway to wait for and a check would race the reload.
func newEdgeRulesFixture(t *testing.T, slug string) *normalPathFixture {
	t.Helper()
	f := newNormalPathFixtureWithRequest(t, slug, api.PlanHobby, 0,
		api.CreateAppRequest{Slug: slug, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false)},
		"FAAS_E2E_GATEWAY_NODE_NAME="+state.DefaultLocalNodeName)
	if f == nil {
		return nil
	}
	var serving int
	if err := f.h.Pool.QueryRow(f.ctx, `select count(*) from compute_nodes
		where active and role in ('compute-node', 'compute-only') and coalesce(gateway_target_url, '') <> ''`).Scan(&serving); err != nil || serving != 1 {
		t.Fatalf("serving gateways = %d (err %v), want the fixture's one", serving, err)
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)
	return f
}

// createEdgeRuleViaAPI creates a rule on slug's app and returns it. The API
// answers 201 only after every serving gateway applied the change, so the
// caller can check public traffic at once.
func createEdgeRuleViaAPI(t *testing.T, f *normalPathFixture, slug string, req map[string]any) api.EdgeRuleResponse {
	t.Helper()
	body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req)
	if code != http.StatusCreated {
		t.Fatalf("create %v rule %v: status=%d body=%s", req["kind"], req["match_path"], code, body)
	}
	var rule api.EdgeRuleResponse
	if err := json.Unmarshal(body, &rule); err != nil {
		t.Fatalf("decode rule: %v body=%s", err, body)
	}
	return rule
}

// edgeRuleRequest is a rule on every host of the fixture's app.
func edgeRuleRequest(kind, matchPath string, action map[string]any, extra ...map[string]any) map[string]any {
	req := map[string]any{"kind": kind, "match_host": api.EdgeRuleAppHosts, "match_path": matchPath, "action": action}
	for _, m := range extra {
		for k, v := range m {
			req[k] = v
		}
	}
	return req
}

// latestEdgeRuleVersion returns the app's current rule-set version.
func latestEdgeRuleVersion(t *testing.T, f *normalPathFixture) int {
	t.Helper()
	body, code := doReq(t, f.h, f.key, http.MethodGet, "/v1/apps/"+f.app.Slug+"/edge-rules/versions", nil)
	if code != http.StatusOK {
		t.Fatalf("list versions: status=%d body=%s", code, body)
	}
	var versions []api.EdgeRuleSetVersionResponse
	if err := json.Unmarshal(body, &versions); err != nil {
		t.Fatalf("decode versions: %v body=%s", err, body)
	}
	latest := 0
	for _, v := range versions {
		latest = max(latest, v.Version)
	}
	if latest == 0 {
		t.Fatalf("no rule-set version recorded: %s", body)
	}
	return latest
}

// TestE2E_EdgeRules_KindsActOnPublicTraffic creates one rule of each kind a
// customer gets on Hobby through the API and checks its effect on the app's
// public URL, then deletes one and checks the effect is gone.
func TestE2E_EdgeRules_KindsActOnPublicTraffic(t *testing.T) {
	f := newEdgeRulesFixture(t, "edge-kinds")
	if f == nil {
		return
	}
	slug := f.app.Slug
	get := func(method, path string, body any, headers map[string]string) (http.Header, []byte, int) {
		t.Helper()
		return edgeRetry(t, method+" "+path, func() (http.Header, []byte, int) {
			return doReqHeaders(t, f.h, f.host, method, path, body, headers)
		})
	}

	redirect := createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("redirect", "/old", map[string]any{"status_code": 302, "to": "/new"}))
	if hdr, body, code := get(http.MethodGet, "/old", nil, nil); code != http.StatusFound || hdr.Get("Location") != "/new" {
		t.Fatalf("redirect: status=%d Location=%q body=%s", code, hdr.Get("Location"), body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("rewrite", "/api/**", map[string]any{"from": "/api", "to": "/v1"}))
	if _, body, code := get(http.MethodGet, "/api/users", nil, nil); code != http.StatusOK {
		t.Fatalf("rewrite: status=%d body=%s", code, body)
	}
	if got := f.vmmd.LastRequest(); got.GetRequestUri() != "/v1/users" {
		t.Fatalf("rewrite: app saw %q, want /v1/users", got.GetRequestUri())
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("headers", "/**", map[string]any{
		"response_headers": []map[string]any{{"name": "X-Edge-E2E", "value": "on", "action": "set"}},
	}))
	if hdr, body, code := get(http.MethodGet, "/", nil, nil); code != http.StatusOK || hdr.Get("X-Edge-E2E") != "on" {
		t.Fatalf("headers: status=%d X-Edge-E2E=%q body=%s", code, hdr.Get("X-Edge-E2E"), body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("cors", "/**", map[string]any{
		"allow_origins": []string{"https://app.example.com"}, "allow_methods": []string{"GET"},
	}))
	preflight := map[string]string{"Origin": "https://app.example.com", "Access-Control-Request-Method": "GET"}
	if hdr, body, code := get(http.MethodOptions, "/", nil, preflight); code != http.StatusNoContent || hdr.Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatalf("cors preflight: status=%d ACAO=%q body=%s", code, hdr.Get("Access-Control-Allow-Origin"), body)
	}

	// A GET-only refusing rule also covers HEAD, which apps answer with their
	// GET handler; other methods pass (amendment 2026-10-28).
	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("ip", "/admin/**", map[string]any{"deny": []string{"0.0.0.0/0", "::/0"}},
		map[string]any{"match_methods": []string{"get"}}))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if _, body, code := get(method, "/admin/x", nil, nil); code != http.StatusForbidden {
			t.Fatalf("ip %s: status=%d body=%s, want 403", method, code, body)
		} else if method == http.MethodGet && problemCode(body) != api.CodeIPDenied {
			t.Fatalf("ip: code=%q, want %s", problemCode(body), api.CodeIPDenied)
		}
	}
	if _, body, code := get(http.MethodPost, "/admin/x", map[string]any{}, nil); code != http.StatusOK {
		t.Fatalf("ip POST: status=%d body=%s, want the app", code, body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("limit", "/upload", map[string]any{"max_body_bytes": 16}))
	if _, body, code := get(http.MethodPost, "/upload", strings.Repeat("x", 64), nil); code != http.StatusRequestEntityTooLarge || problemCode(body) != api.CodeRequestTooLarge {
		t.Fatalf("limit: status=%d body=%s, want 413", code, body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("validate", "/users", map[string]any{
		"schema":        map[string]any{"type": "object", "required": []string{"email"}, "properties": map[string]any{"email": map[string]any{"type": "string"}}},
		"content_types": []string{"application/json"},
	}, map[string]any{"match_methods": []string{"POST"}}))
	if _, body, code := get(http.MethodPost, "/users", map[string]any{}, nil); code != http.StatusUnprocessableEntity || problemCode(body) != api.CodeRequestValidationFailed {
		t.Fatalf("validate refuse: status=%d body=%s, want 422", code, body)
	}
	if _, body, code := get(http.MethodPost, "/users", map[string]any{"email": "a@example.com"}, nil); code != http.StatusOK {
		t.Fatalf("validate pass: status=%d body=%s", code, body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("maintenance", "/billing/**", map[string]any{"retry_after_seconds": 5, "message": "billing upgrade"}))
	if hdr, body, code := get(http.MethodGet, "/billing/x", nil, nil); code != http.StatusServiceUnavailable || problemCode(body) != api.CodeEdgeRuleMaintenance || hdr.Get("Retry-After") != "5" {
		t.Fatalf("maintenance: status=%d Retry-After=%q body=%s", code, hdr.Get("Retry-After"), body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("throttle", "/login", map[string]any{"requests_per_second": 0.1, "burst": 1}))
	if _, body, code := get(http.MethodGet, "/login", nil, nil); code != http.StatusOK {
		t.Fatalf("throttle first: status=%d body=%s", code, body)
	}
	if _, body, code := get(http.MethodGet, "/login", nil, nil); code != http.StatusTooManyRequests {
		t.Fatalf("throttle second: status=%d body=%s, want 429", code, body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("jwt", "/private/**", map[string]any{
		"issuer": "https://idp.example.com/", "jwks_url": "https://idp.example.com/.well-known/jwks.json",
		"algorithms": []string{"RS256"}, "audience": []string{"edge-e2e"},
	}))
	if hdr, body, code := get(http.MethodGet, "/private/x", nil, nil); code != http.StatusUnauthorized || !strings.HasPrefix(hdr.Get("WWW-Authenticate"), "Bearer") {
		t.Fatalf("jwt: status=%d WWW-Authenticate=%q body=%s, want 401", code, hdr.Get("WWW-Authenticate"), body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("cache", "/catalog", map[string]any{"max_age_seconds": 60}))
	before := len(normalPathInstancesFor(f, "/catalog"))
	for i := range 2 {
		if _, body, code := get(http.MethodGet, "/catalog", nil, nil); code != http.StatusOK {
			t.Fatalf("cache request %d: status=%d body=%s", i, code, body)
		}
	}
	if reached := len(normalPathInstancesFor(f, "/catalog")) - before; reached != 1 {
		t.Fatalf("cache: the app answered %d of 2 requests, want 1", reached)
	}

	// Deleting a rule takes it off the gateways before the API answers.
	if body, code := doReq(t, f.h, f.key, http.MethodDelete, "/v1/edge-rules/"+redirect.ID, nil); code != http.StatusNoContent {
		t.Fatalf("delete redirect: status=%d body=%s", code, body)
	}
	if _, body, code := get(http.MethodGet, "/old", nil, nil); code != http.StatusOK {
		t.Fatalf("after delete: status=%d body=%s, want the app", code, body)
	}

	// Inputs that used to be accepted and then matched nothing, or moved the
	// client address the IP gates judge, are refused (amendment 2026-10-28).
	for name, req := range map[string]map[string]any{
		"wildcard method": edgeRuleRequest("ip", "/x", map[string]any{"deny": []string{"0.0.0.0/0"}}, map[string]any{"match_methods": []string{"*"}}),
		"X-Forwarded-For": edgeRuleRequest("headers", "/x", map[string]any{
			"request_headers": []map[string]any{{"name": "X-Forwarded-For", "value": "198.51.100.9", "action": "set"}},
		}),
	} {
		if body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules", req); code < 400 || code >= 500 {
			t.Fatalf("%s rule: status=%d body=%s, want a 4xx refusal", name, code, body)
		}
	}
}

// TestE2E_EdgeRules_ListsAndRollback: a list edit reaches the rules that use
// it, a rollback restores an earlier rule set on the gateways, a list in use
// cannot be deleted, and a rollback to a version whose list is gone is
// refused instead of restoring a rule that would never match.
func TestE2E_EdgeRules_ListsAndRollback(t *testing.T) {
	f := newEdgeRulesFixture(t, "edge-lists")
	if f == nil {
		return
	}
	slug := f.app.Slug
	get := func(path string) (http.Header, []byte, int) {
		t.Helper()
		return edgeRetry(t, "GET "+path, func() (http.Header, []byte, int) {
			return doReqHeaders(t, f.h, f.host, http.MethodGet, path, nil)
		})
	}

	if body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/edge-rule-lists", api.CreateEdgeRuleListRequest{
		Name: "blocked", Kind: "ip", Items: []string{"127.0.0.0/8", "::1/128"},
	}); code != http.StatusCreated {
		t.Fatalf("create list: status=%d body=%s", code, body)
	}
	listed := createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("ip", "/listed/**", map[string]any{"deny": []string{"0.0.0.0/0", "::/0"}},
		map[string]any{"match": map[string]any{"field": "client_ip", "op": "in_list", "list": "blocked"}}))
	withList := latestEdgeRuleVersion(t, f)
	if _, body, code := get("/listed/x"); code != http.StatusForbidden {
		t.Fatalf("listed client: status=%d body=%s, want 403", code, body)
	}

	items := []string{"192.0.2.0/24"}
	if body, code := doReq(t, f.h, f.key, http.MethodPatch, "/v1/edge-rule-lists/blocked", api.UpdateEdgeRuleListRequest{Items: &items}); code != http.StatusOK {
		t.Fatalf("edit list: status=%d body=%s", code, body)
	}
	if _, body, code := get("/listed/x"); code != http.StatusOK {
		t.Fatalf("after the list edit: status=%d body=%s, want the app", code, body)
	}

	createEdgeRuleViaAPI(t, f, slug, edgeRuleRequest("headers", "/**", map[string]any{
		"response_headers": []map[string]any{{"name": "X-Later", "value": "yes", "action": "set"}},
	}))
	if hdr, _, _ := get("/"); hdr.Get("X-Later") != "yes" {
		t.Fatal("headers rule not applied")
	}
	if body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules/rollback", api.RollbackEdgeRulesRequest{Version: withList}); code != http.StatusOK {
		t.Fatalf("rollback: status=%d body=%s", code, body)
	}
	if hdr, _, _ := get("/"); hdr.Get("X-Later") != "" {
		t.Fatal("rollback left the later headers rule on the gateways")
	}

	if body, code := doReq(t, f.h, f.key, http.MethodDelete, "/v1/edge-rule-lists/blocked", nil); code != http.StatusConflict || problemCode(body) != api.CodeEdgeRuleListInUse {
		t.Fatalf("delete a list in use: status=%d body=%s, want 409", code, body)
	}
	if body, code := doReq(t, f.h, f.key, http.MethodDelete, "/v1/edge-rules/"+listed.ID, nil); code != http.StatusNoContent {
		t.Fatalf("delete rule: status=%d body=%s", code, body)
	}
	if body, code := doReq(t, f.h, f.key, http.MethodDelete, "/v1/edge-rule-lists/blocked", nil); code != http.StatusNoContent {
		t.Fatalf("delete list: status=%d body=%s", code, body)
	}
	body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps/"+slug+"/edge-rules/rollback", api.RollbackEdgeRulesRequest{Version: withList})
	if code != http.StatusBadRequest || !strings.Contains(string(body), `"blocked"`) {
		t.Fatalf("rollback to a version whose list is gone: status=%d body=%s, want 400 naming the list", code, body)
	}
}

// TestE2E_EdgeRules_DeletedAppRulesStopApplying: a rule on "*" covers every
// host of its account, including other apps'. Once its app is deleted it no
// longer applies to them (amendment 2026-10-28); it used to keep doing so.
func TestE2E_EdgeRules_DeletedAppRulesStopApplying(t *testing.T) {
	f := newEdgeRulesFixture(t, "edge-survivor")
	if f == nil {
		return
	}
	other := "edge-deleted"
	if body, code := doReq(t, f.h, f.key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: other, Type: string(state.AppTypeApp), RequireAuthn: boolPtr(false),
	}); code != http.StatusCreated {
		t.Fatalf("create second app: status=%d body=%s", code, body)
	}
	createEdgeRuleViaAPI(t, f, other, map[string]any{
		"kind": "headers", "match_host": "*", "match_path": "/**",
		"action": map[string]any{"response_headers": []map[string]any{{"name": "X-From-Other-App", "value": "yes", "action": "set"}}},
	})
	if hdr, body, _ := edgeRetry(t, "GET /", func() (http.Header, []byte, int) {
		return doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
	}); hdr.Get("X-From-Other-App") != "yes" {
		t.Fatalf("the other app's * rule does not cover this app's host: %v %s", hdr, body)
	}

	if body, code := doReq(t, f.h, f.key, http.MethodDelete, "/v1/apps/"+other, nil); code >= 300 {
		t.Fatalf("delete the other app: status=%d body=%s", code, body)
	}
	// A gateway that never served the deleted app drops its account's
	// compiled rules when they expire (30 s).
	deadline := time.Now().Add(45 * time.Second)
	for {
		hdr, body, code := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil)
		if code == http.StatusOK && hdr.Get("X-From-Other-App") == "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the deleted app's rule still applies after 45s: status=%d X-From-Other-App=%q body=%s", code, hdr.Get("X-From-Other-App"), body)
		}
		time.Sleep(time.Second)
	}
}
