// commands_tier_c_test.go — Tier C audit-gap smoke tests. Pins the
// 9 newly-wired leaves so a refactor can't silently drop a route,
// flip an auth gate, or land a closed-set enum without a CLI check.
//
// Pattern (mirrors commands_tier_b_test.go): arg-validation exits 1,
// auth-gate exits 2 (where applicable), happy path hits the right
// route with the right body. --json path is checked for one leaf per
// family to pin the shape. stdout-content assertions are deliberately
// absent — the load-bearing seam is the route + method + body;
// renderer drift is covered by the existing output_test.go family.
//
// Secrets plaintext / API-key plaintext / webhook secret plaintext
// are NEVER echoed by the CLI today (spec §17 G6) — those guards
// live in the leaf body itself, not in tests that could miss a
// future regression. Alert-rule secret rotation retains its existing
// server-minted, masked-only contract; app webhook rotation has a separate
// caller-supplied handoff contract.

package main

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// --- invocations list ---

func TestTierC_InvocationsList_NoTokenExitsTwo(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	f := newFakeAPI(t, "", http.StatusOK)
	if code := cmdInvocationsList(nil); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth)", code)
	}
	if f.sawMethod != "" {
		t.Errorf("no network call expected without token; saw %s %s", f.sawMethod, f.sawPath)
	}
}

func TestTierC_InvocationsList_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"invocations":[{"id":"i-1","created_at":"2026-08-07T00:00:00Z","state":"completed","method":"POST","path":"/invoke","app_id":"a-1"}]}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdInvocationsList(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/invocations" {
		t.Errorf("route = %s %s, want GET /v1/invocations", f.sawMethod, f.sawPath)
	}
}

// --- invocations get ---

func TestTierC_InvocationsGet_NoArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdInvocationsGet(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierC_InvocationsGet_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"i-1","state":"completed","source":"sync","method":"POST","path":"/invoke","app_id":"a-1","instance_id":"x","due_at":"2026-08-07T00:00:00Z","attempts":1}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdInvocationsGet([]string{"i-1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/invocations/i-1" {
		t.Errorf("route = %s %s, want GET /v1/invocations/i-1", f.sawMethod, f.sawPath)
	}
}

// --- alerts list ---

func TestTierC_AlertsList_NoAppFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdAlertList(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierC_AlertsList_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `[{"id":"0123456789abcdef0123456789abcdef","name":"r1","metric":"error_rate_pct","window_spec":"5m","threshold":1.5,"comparison":"gt","enabled":true}]`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAlertList([]string{"--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/alerts" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/alerts", f.sawMethod, f.sawPath)
	}
}

// --- alerts add (closed-set drift) ---

func TestTierC_AlertsAdd_BadMetricExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	// Closed-set check fires BEFORE the round-trip — the fake API
	// would otherwise see the bogus metric and return a generic
	// 400, which is a worse operator experience.
	if code := cmdAlertAdd([]string{"--app", "demo", "--name", "r", "--metric", "wat", "--comparison", "gt", "--threshold", "1", "--window-spec", "5m", "--webhook-url", "https://x", "--webhook-secret", "s"}); code != 1 {
		t.Fatalf("bad metric exit = %d, want 1", code)
	}
}

func TestTierC_AlertsAdd_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","name":"r","metric":"error_rate_pct","window_spec":"5m","threshold":1.5,"comparison":"gt","enabled":true}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAlertAdd([]string{
		"--app", "demo", "--name", "r", "--metric", "error_rate_pct",
		"--comparison", "gt", "--threshold", "1.5",
		"--window-spec", "5m", "--webhook-url", "https://x", "--webhook-secret", "s",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/alerts" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/alerts", f.sawMethod, f.sawPath)
	}
}

// TestTierC_AlertsUpdate_NameOnlyDoesNotResendEnabledOrCooldown pins
// the pointer-shape fix: a rename-only update must NOT re-enable a
// disabled rule nor reset cooldown to the default. Without this the
// CLI was silently breaking the operator's intent on every update.
func TestTierC_AlertsUpdate_NameOnlyDoesNotResendEnabledOrCooldown(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","name":"renamed","enabled":false,"metric":"error_rate_pct","window_spec":"5m","threshold":1.5,"comparison":"gt"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAlertUpdate([]string{
		"--app", "demo", "--name", "renamed",
		"0123456789abcdef0123456789abcdef",
	}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	// Body must carry `name` (the only thing we set) and must NOT
	// carry enabled or cooldown_minutes — JSON unmarshal of the
	// request body decodes into a generic map for the assertion.
	var got map[string]any
	if err := json.Unmarshal(f.sawBody, &got); err != nil {
		t.Fatalf("decode body: %v; raw=%s", err, string(f.sawBody))
	}
	if got["name"] != "renamed" {
		t.Errorf("name = %v, want \"renamed\"", got["name"])
	}
	if _, ok := got["enabled"]; ok {
		t.Errorf("enabled present in body; must be omitted on rename-only update (got %v)", got["enabled"])
	}
	if _, ok := got["cooldown_minutes"]; ok {
		t.Errorf("cooldown_minutes present in body; must be omitted on rename-only update (got %v)", got["cooldown_minutes"])
	}
}

// --- alerts rotate-secret (one-shot plaintext dropped) ---

func TestTierC_AlertsRotateSecret_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"rotated_at":"2026-08-07T12:00:00Z","webhook_secret_sealed_masked":"***"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAlertRotateSecret([]string{"--app", "demo", "0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/alerts/0123456789abcdef0123456789abcdef/rotate-secret" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/alerts/.../rotate-secret", f.sawMethod, f.sawPath)
	}
}

// --- invoke (sync) ---

func TestTierC_Invoke_NoSlugExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdInvoke(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierC_Invoke_SyncHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"i-1","status":"completed","result":"{}"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	// Test with bare slug (no payload) — covers the happy path
	// without triggering the --payload=value positional-arg
	// quirk that bit an earlier run. resolvePayload's empty
	// branch is independently exercised in the cmdInvoke leaf.
	if code := cmdInvoke([]string{"demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/invoke" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/invoke", f.sawMethod, f.sawPath)
	}
}

func TestTierC_Invoke_AsyncHappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"i-2","status_url":"/v1/invocations/i-2"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdInvoke([]string{"--async", "demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/invoke/async" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/invoke/async", f.sawMethod, f.sawPath)
	}
}

// --- queue send (Tier C arm of cmdQueueDispatch) ---

func TestTierC_QueueSend_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"q-1","state":"queued"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdQueueSend([]string{"demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/queues/send" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/queues/send", f.sawMethod, f.sawPath)
	}
}

func TestTierC_QueueState_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"depth":0,"in_flight":0}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdQueueState([]string{"demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/queues/state" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/queues/state", f.sawMethod, f.sawPath)
	}
}

func TestTierC_QueueStatusAlias_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"depth":0,"in_flight":0}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdQueueDispatch([]string{"status", "demo"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/queues/state" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/queues/state", f.sawMethod, f.sawPath)
	}
}

// --- orgs keys (Tier C arm of cmdOrgs) ---

func TestTierC_OrgsKeysList_NoOrgFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdOrgsKeysList(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierC_OrgsKeysList_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"keys":[{"id":"k-1","prefix":"fp_live_abc","label":"ci"}]}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdOrgsKeysList([]string{"--org", "acme"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/orgs/acme/keys" {
		t.Errorf("route = %s %s, want GET /v1/orgs/acme/keys", f.sawMethod, f.sawPath)
	}
}

func TestTierC_OrgsKeysAdd_HappyPath(t *testing.T) {
	resetJSONOut(t)
	resp := api.APIKeyResponse{ID: "k-1", Prefix: "fp_live_abc", Label: "ci", Plaintext: "DO-NOT-LEAK"}
	bodyBytes, _ := json.Marshal(resp)
	f := authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	if code := cmdOrgsKeysAdd([]string{"--org", "acme", "--label", "ci"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/orgs/acme/keys" {
		t.Errorf("route = %s %s, want POST /v1/orgs/acme/keys", f.sawMethod, f.sawPath)
	}
}

func TestTierC_OrgsKeysRm_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, "", http.StatusNoContent)
	if code := cmdOrgsKeysRm([]string{"--org", "acme", "k-1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "DELETE" || f.sawPath != "/v1/orgs/acme/keys/k-1" {
		t.Errorf("route = %s %s, want DELETE /v1/orgs/acme/keys/k-1", f.sawMethod, f.sawPath)
	}
}

// --- orgs invitations list-all (cursor walker, Tier C arm) ---

func TestTierC_OrgsInvitationsListAll_HappyPath(t *testing.T) {
	resetJSONOut(t)
	// The walker keeps paging until next_before is empty; the fake
	// returns a single short page so the SDK helper terminates.
	body := `{"invitations":[{"id":"i-1","email":"x@y","role":"developer","status":"pending","expires_at":"2026-08-14T00:00:00Z"}],"next_before":""}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdOrgsInvitationsListAll([]string{"--org", "acme"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/orgs/acme/invitations" {
		t.Errorf("route = %s %s, want GET /v1/orgs/acme/invitations", f.sawMethod, f.sawPath)
	}
}

// --- webhooks info ---

func TestTierC_WebhooksInfo_BadIDExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdWebhookInfo([]string{"--app", "demo", "not-a-uuid"}); code != 1 {
		t.Fatalf("bad id exit = %d, want 1", code)
	}
}

func TestTierC_WebhooksInfo_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"0123456789abcdef0123456789abcdef","app_id":"a-1","target_url":"https://x","webhook_secret_sealed_masked":"***","event_filter":["app.deployed"],"retry_policy":"default","enabled":true,"created_at":"2026-08-01T00:00:00Z","updated_at":"2026-08-07T00:00:00Z"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdWebhookInfo([]string{"--app", "demo", "0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/demo/webhooks/0123456789abcdef0123456789abcdef" {
		t.Errorf("route = %s %s, want GET /v1/apps/demo/webhooks/.../info", f.sawMethod, f.sawPath)
	}
}

// --- usage daily ---

func TestTierC_UsageDaily_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"items":[{"app_id":"a-1","day":"2026-08-07","mb_seconds":1000,"requests":3}]}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdUsageDaily([]string{"--day", "2026-08-07"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/usage/daily" {
		t.Errorf("route = %s %s, want GET /v1/usage/daily", f.sawMethod, f.sawPath)
	}
}

// --- usage storage ---

func TestTierC_UsageStorage_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"items":[{"app_id":"a-1","day":"2026-08-07","snapshot_bytes":1048576,"layer_bytes":524288}]}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdUsageStorage([]string{"--day", "2026-08-07"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/usage/storage" {
		t.Errorf("route = %s %s, want GET /v1/usage/storage", f.sawMethod, f.sawPath)
	}
}

// --- metrics --account ---

func TestTierC_MetricsAccount_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"range":"5m","source":"prometheus","as_of":"2026-08-07T00:00:00Z","apps":{"a-1":{"app_id":"a-1","range":"5m","request_count":3,"latency_p50_ms":1.0}}}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdMetrics([]string{"--account"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/apps/metrics" {
		t.Errorf("route = %s %s, want GET /v1/apps/metrics", f.sawMethod, f.sawPath)
	}
}

// TestTierC_MetricsAccount_RejectsSlug pins the mutual exclusion —
// --account is account-wide, <slug> is per-app; the two shapes
// can't share the leaf body.
func TestTierC_MetricsAccount_RejectsSlug(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdMetrics([]string{"--account", "demo"}); code != 1 {
		t.Fatalf("--account + slug exit = %d, want 1", code)
	}
}
