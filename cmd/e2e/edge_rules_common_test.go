// edge_rules_common_test.go — shared helpers for the per-kind edge-rules
// e2e files (rewrite / redirect / headers / cors / jwt / ip; see ADR-091
// D18 / issue #561 PR 6).
//
// The package-level doReq in quota_e2e_test.go is intentionally NOT
// modified: 138 call sites across cmd/e2e/ rely on its (body, status)
// shape and none captures resp.Header. The new doReqHeaders below is
// additive; gatewayd-bound requests use h.GatewayURL and set req.Host
// explicitly because no DNS is wired up in the harness (mirroring
// deploy_wake_metal_test.go:doGetWithHost at lines 372-388).
//
// Bitmask for all six e2e files: e2etest.APID | e2etest.Gatewayd.
// Edge-rule handlers all run at `haveApp` (spec §4.1.2) AFTER
// Backend.Lookup; per-kind tests therefore seed a `kind=route`
// substitute (synthetic host → real test app slug) as a precondition.
// D20.1 defers `kind=route` itself to a DeployWake-bitmask follow-on.

package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
)

// gatewayReqOptions captures the gateway-bound knobs that doReqHeaders
// needs but the apid-bound package-level doReq does not: the explicit
// Host header (the gateway router keys on it) and an optional pre-built
// body (the rewrite/headers/cors cases send nil; the jwt case sends no
// body but a Bearer header).
type gatewayReqOptions struct {
	Host          string
	Authorization string
	Extra         map[string]string
}

// gatewayReq is the gateway-bound counterpart to package-level doReq.
// Always uses h.GatewayURL (gatewayd-internal serves plain HTTP per
// Tier A7; no TLS), always sets req.Host for the host-based router.
// Returns (resp.Header, body, status) so the per-kind tests can
// assert on stamped headers (Access-Control-*, Location, X-*,
// WWW-Authenticate) without re-issuing the request. The body is
// drained inside this function and Body.Close() runs via defer
// before return — bodyclose's plugin tracks the lifecycle
// locally rather than across the function boundary.
func gatewayReq(t *testing.T, h *e2etest.Harness, method, path string, body any, opts gatewayReqOptions) (http.Header, []byte, int) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.GatewayURL+path, r)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Host = opts.Host
	if opts.Authorization != "" {
		req.Header.Set("Authorization", opts.Authorization)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range opts.Extra {
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(req.Context(), 10*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	client := *h.HTTPClient()
	// Edge-rule tests assert redirect status and Location themselves. The
	// default client follows 3xx responses and turns a local contract check
	// into an external DNS request.
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s (Host=%s): %v", method, path, opts.Host, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.Header, b, resp.StatusCode
}

// doReqHeaders is the helper that captures resp.Header alongside
// (body, status). Returns (header, body, status) so a test can assert
// on stamped headers (Location, Access-Control-Allow-Origin, X-*, etc.)
// without re-issuing the request.
//
// host is the Host header the gateway router keys on. method is
// GET/POST/OPTIONS/PUT/DELETE. path is the request path (no leading
// host; h.GatewayURL supplies the scheme + IP). body is JSON-marshalled
// if non-nil; pass nil for GET/OPTIONS. extraHeaders are set last so
// they can override the default Content-Type (rare; needed for
// application/x-www-form-urlencoded preflight, etc.).
func doReqHeaders(t *testing.T, h *e2etest.Harness, host, method, path string, body any, extraHeaders ...map[string]string) (http.Header, []byte, int) {
	t.Helper()
	var merged map[string]string
	if len(extraHeaders) > 0 {
		merged = make(map[string]string)
		for _, m := range extraHeaders {
			for k, v := range m {
				merged[k] = v
			}
		}
	}
	return gatewayReq(t, h, method, path, body, gatewayReqOptions{
		Host:  host,
		Extra: merged,
	})
}

// seedEdgeRuleDirect inserts one edge_rule row via the test pool,
// bypassing apid-Validate. Used by every per-kind e2e test in this
// directory to set up the production-shape pipeline without going
// through the API surface. Returns the rule ID.
//
// match_path is hardcoded to '*' (the catch-all sentinel the
// path-glob matcher honours per pkg/gateway/edge_rules.go:759-768).
// match_methods is hardcoded to '{}' (no method filter).
func seedEdgeRuleDirect(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	accountID, appID, host string,
	kind state.EdgeRuleKind,
	action any,
) string {
	t.Helper()
	actionBytes, err := json.Marshal(action)
	if err != nil {
		t.Fatalf("marshal action: %v", err)
	}
	var ruleID string
	err = pool.QueryRow(ctx, `
		insert into edge_rules (
			account_id, app_id, match_host, match_path,
			match_methods, priority, enabled, kind, action
		) values (
			$1, $2, $3, '*',
			'{}'::text[], 0, true, $4, $5::jsonb
		)
		returning id`,
		accountID, appID, host, string(kind), actionBytes,
	).Scan(&ruleID)
	if err != nil {
		t.Fatalf("insert edge_rule kind=%s host=%s: %v", kind, host, err)
	}
	return ruleID
}

// accountIDFromKey looks up the account_id for the seeded API key
// by SHA-256-hashing the plaintext and joining via
// api_keys.key_sha256 → accounts.id. The harness's SeedAccount
// inserts the row with the same SHA-256 hash (pkg/api/apikey.go:30).
func accountIDFromKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, key string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(key))
	var accountID string
	err := pool.QueryRow(ctx, `
		select k.account_id from api_keys k
		where k.key_sha256 = $1
		limit 1`, sum[:]).Scan(&accountID)
	if err != nil {
		t.Fatalf("lookup account for key: %v", err)
	}
	return accountID
}

// resetEdgeRuleCache POSTs to gatewayd-internal's /admin/edge-rules/reset
// control-plane endpoint so a freshly-inserted rule is observed on
// the next request (gatewayd subscribes to edge_rule_changed pg_notify,
// but the seed bypasses that path). If the endpoint isn't wired the
// call is non-fatal — the test still observes whatever the cache
// state was at boot.
func resetEdgeRuleCache(t *testing.T, h *e2etest.Harness) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost,
		h.GatewayControlURL+"/admin/edge-rules/reset", nil)
	if err != nil {
		t.Fatalf("new reset req: %v", err)
	}
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Logf("edge-rule reset endpoint not reachable (non-fatal): %v", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("edge-rule reset: status=%d, want 204", resp.StatusCode)
	}
}

// assertBackendFallthrough accepts the two valid harness outcomes after an
// edge rule passes: a router miss, or a capacity response when the routed app
// reaches the wake path without schedd/vmmd in this test bitmask.
func assertBackendFallthrough(t *testing.T, status int, body []byte) {
	t.Helper()
	if status == http.StatusNotFound {
		return
	}
	var problem api.Problem
	if status == http.StatusServiceUnavailable && json.Unmarshal(body, &problem) == nil && problem.Code == api.CodeCapacity {
		return
	}
	t.Errorf("backend fallthrough: status=%d body=%s", status, body)
}

func problemCode(body []byte) string {
	var problem api.Problem
	_ = json.Unmarshal(body, &problem)
	return problem.Code
}

// seedRouteSubstitute seeds the `kind=route` precondition rule that
// every per-kind e2e test needs to get past Backend.Lookup on a
// synthetic host. Without this, ServeHTTP returns 404 at handler.go
// :2261 before any non-route edge rule fires.
func seedRouteSubstitute(t *testing.T, ctx context.Context, pool *pgxpool.Pool,
	accountID, appID, host, appSlug string) string {
	return seedEdgeRuleDirect(t, ctx, pool, accountID, appID, host,
		state.EdgeRuleKindRoute,
		map[string]any{
			"kind": "route",
			"route": map[string]any{
				"target_app_slug": appSlug,
			},
		},
	)
}
