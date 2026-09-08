// edge_rules_ip_e2e_test.go — D18 per-kind e2e for `kind=ip`.
// Bitmask: APID | Gatewayd. See edge_rules_common_test.go for the
// kind=route-substitute precondition pattern.
//
// Client-IP provenance (ADR-091 D13): the gateway reads the single
// trusted XFF entry from pkg/gateway/internal_proxy.go:286. The
// e2e harness hits gatewayd-internal directly (no gatewayd-public
// in front), so r.RemoteAddr is the loopback test client IP. The
// trust boundary is gatewayd-public → gatewayd-internal; the
// e2e tests verify the kind=ip *matcher*, not the XFF trust. We
// use synthetic X-Forwarded-For values that the gateway's IP rule
// evaluates directly.

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

func TestEdgeRulesIP_E2E(t *testing.T) {
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

	slug := "ip-test-app"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: slug})
	if len(createRec) == 0 {
		t.Fatalf("create app: empty response")
	}
	var app api.AppResponse
	if err := json.Unmarshal(createRec, &app); err != nil {
		t.Fatalf("decode app: %v body=%s", err, createRec)
	}

	synthHost := "edgectl-ip.apps.test.example"

	seedRouteSubstitute(t, context.Background(), pool,
		accountID, app.ID, synthHost, slug)

	// Rule under test: kind=ip, Allow=[10.0.0.0/8], Deny=[192.0.2.0/24].
	// The Allow-list takes precedence when non-empty; Deny is
	// evaluated AFTER allow (per pkg/state/types.go:3113-3119 doc).
	seedEdgeRuleDirect(t, context.Background(), pool,
		accountID, app.ID, synthHost,
		state.EdgeRuleKindIP,
		map[string]any{
			"kind": "ip",
			"ip": map[string]any{
				"allow": []string{"10.0.0.0/8"},
				"deny":  []string{"192.0.2.0/24"},
			},
		},
	)

	resetEdgeRuleCache(t, h)

	// Happy path: X-Forwarded-For 10.0.0.1 matches the allow-list
	// → rule fires "match" outcome, no short-circuit, fall through
	// to Backend.Pick → 404 (no routable target).
	_, _, status := doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"X-Forwarded-For": "10.0.0.1"})
	if status != http.StatusNotFound {
		t.Errorf("kind=ip allow-match: status=%d, want 404 (Backend.Pick miss after IP pass)", status)
	}

	// Negative path: X-Forwarded-For 192.0.2.1 hits the Deny list
	// → 403 short-circuit (handler.go:2311).
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"X-Forwarded-For": "192.0.2.1"})
	if status != http.StatusForbidden {
		t.Errorf("kind=ip deny: status=%d, want 403", status)
	}

	// Negative path: X-Forwarded-For 8.8.8.8 (not in allow-list).
	// Allow non-empty + no match = implicit deny (ADR-091 D-list
	// implementation: pkg/state/types.go:3115) → 403.
	_, _, status = doReqHeaders(t, h, synthHost, http.MethodGet, "/", nil,
		map[string]string{"X-Forwarded-For": "8.8.8.8"})
	if status != http.StatusForbidden {
		t.Errorf("kind=ip implicit-deny: status=%d, want 403 (allow non-empty + no match)", status)
	}
}
