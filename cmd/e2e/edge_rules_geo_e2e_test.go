// edge_rules_geo_e2e_test.go — per-kind e2e for `kind=geo` (ADR-091 D21).
// Bitmask: APID | Gatewayd. See edge_rules_common_test.go for the
// kind=route-substitute precondition pattern.
//
// ## Fail-open posture in the e2e harness
//
// The geo gate (`pkg/gateway/handler.go:2311`) consults a
// `*geoip.Reader` whose on-disk `.mmdb` is loaded at gatewayd boot.
// The e2e harness does NOT ship a real DB-IP file (the file is ~5 MB
// and CC-BY-4.0 — we license-track it via the operator's own
// deployment, not via the test corpus). When the file is missing,
// the reader is nil and applyEdgeRuleGeo fail-opens: the rule
// does NOT short-circuit. This e2e test asserts that posture —
// a kind=geo rule under a missing DB behaves identically to "no
// rule" (request passes through to Backend.Pick → 404 because the
// test target is a synthetic host with no production mapping).
//
// The positive-path gate (deny a country that matches) is covered
// by unit tests in pkg/gateway/edge_rules_geo_test.go. The full
// positive-path coverage requires a synthesised MMDB and is
// deferred to PR-2 (issue #562 follow-up).
//
// ## Quota path
//
// We exercise the per-kind quota path through the apid HTTP surface
// (POST /v1/edge-rules) on a Free-plan account, where
// EdgeRulesGeoPerApp = 1. The 2nd geo create returns 403
// `plan_edge_rule_kind_quota_reached`.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEdgeRulesGeo_E2E_FailOpenUnderMissingDB pins the §4.1.2.8b
// fail-open posture: with no .mmdb loaded, a kind=geo rule does
// NOT short-circuit the request. The synthetic host's only rule
// is the geo allowlist (deny all except DE); without a DB the
// gate skips and the request falls through to Backend.Pick
// (404 — synthetic host, no production mapping).
func TestEdgeRulesGeo_E2E_FailOpenUnderMissingDB(t *testing.T) {
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

	slug := "geo-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-geo.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Geo rule: deny=US, allow=[DE, FR]. With a missing DB, the
	// gate fail-opens and the request passes through regardless
	// of country.
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindGeo,
		map[string]any{
			"kind": "geo",
			"geo": map[string]any{
				"allow": []string{"DE", "FR"},
				"deny":  []string{"US"},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Request 1: X-Forwarded-For 8.8.8.8 (US) — under a loaded DB
	// this would 403 via the Deny list. Under fail-open, it falls
	// through to Backend.Pick (404 because the synthetic host has
	// no production target).
	_, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"X-Forwarded-For": "8.8.8.8"})
	if status != http.StatusNotFound {
		t.Errorf("fail-open on US-IP: status=%d, want 404 (gate fail-opened)", status)
	}

	// Request 2: DE-bound IP (would PASS allow-list under loaded DB).
	// Same posture: 404 because the gate didn't fire.
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"X-Forwarded-For": "9.9.9.9"})
	if status != http.StatusNotFound {
		t.Errorf("fail-open on DE-IP: status=%d, want 404 (gate fail-opened)", status)
	}
}

// TestEdgeRulesGeo_E2E_FreeQuotaRejected pins the per-kind quota
// path (ADR-091 D22) at the apid HTTP layer. Free plan has
// EdgeRulesGeoPerApp = 1; the 2nd create returns 403
// `plan_edge_rule_kind_quota_reached`. The general cap
// (EdgeRulesPerApp) is also 1 on Free, so this test relies on the
// per-kind check running AFTER the general check — Free with 1
// rule total and the 2nd being a `geo` is the exact trip-wire:
// the general cap (1 vs 2 attempted) trips first if the order is
// wrong, and only the per-kind code surfaces when the order is
// right. Hobby would mask the order: Hobby's EdgeRulesPerApp=5
// gives the per-kind check room to fire without the general cap
// being saturated. The 403 body assertion on
// `plan_edge_rule_kind_quota_reached` is the load-bearing check
// that distinguishes a passing test for the right reason from a
// passing test for the general-cap reason.
func TestEdgeRulesGeo_E2E_FreeQuotaRejected(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	h := e2etest.StartWithEnv(t, pool, e2etest.APID, nil)
	key := h.SeedAccount(context.Background(), api.PlanFree)
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	slug := "geo-quota-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	// First create: under cap → 201.
	host1 := "free-geo-1.apps.test.example"
	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, host1, slug)

	body1, _ := json.Marshal(api.CreateEdgeRuleRequest{
		MatchHost: host1,
		Kind:      string(state.EdgeRuleKindGeo),
		Action:    mustMarshalGeoAction(t, []string{"DE"}, nil),
	})
	_, status1 := doReq(t, h, key, http.MethodPost, "/v1/edge-rules", json.RawMessage(body1))
	if status1 != http.StatusCreated {
		t.Fatalf("first geo create: status=%d, want 201 (under cap)", status1)
	}

	// Second create: at cap → 403 with the per-kind code.
	host2 := "free-geo-2.apps.test.example"
	body2, _ := json.Marshal(api.CreateEdgeRuleRequest{
		MatchHost: host2,
		Kind:      string(state.EdgeRuleKindGeo),
		Action:    mustMarshalGeoAction(t, []string{"FR"}, nil),
	})
	raw2, status2 := doReq(t, h, key, http.MethodPost, "/v1/edge-rules", json.RawMessage(body2))
	if status2 != http.StatusForbidden {
		t.Errorf("second geo create: status=%d, want 403 (Free per-kind cap=1)", status2)
	}
	if !strings.Contains(string(raw2), "plan_edge_rule_kind_quota_reached") {
		t.Errorf("second geo create body did not surface the per-kind code: %s", raw2)
	}
}

// mustMarshalGeoAction serialises an EdgeRuleGeoAction into a
// json.RawMessage for the wire. Centralises the literal shape so
// the test mirrors what apid-Validate expects.
func mustMarshalGeoAction(t *testing.T, allow, deny []string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(api.EdgeRuleGeoAction{Allow: allow, Deny: deny})
	if err != nil {
		t.Fatalf("marshal geo action: %v", err)
	}
	return raw
}
