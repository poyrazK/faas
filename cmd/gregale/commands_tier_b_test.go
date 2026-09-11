// commands_tier_b_test.go — Tier B audit-gap smoke tests. Pins
// the 9 newly-wired leaves (7 CLI-only — sessions + auth capabilities
// were dropped, see below) so a refactor can't silently drop a
// route or flip the auth gate.
//
// `gregale sessions` and `gregale auth capabilities` were removed
// before merge — the underlying routes are session-cookie-only
// (handlers_sessions.go reads sessionFrom(r), which is cookie-only
// per pkg/auth/middleware/context.go:141; /v1/auth/capabilities
// is mounted behind sessionAuth at server.go:1085) so a bearer-key
// CLI caller always hits 401. The dashboard remains the only
// supported surface for those operations.
//
// Pattern (mirrors commands_mfa_test.go): arg-validation exits 1,
// auth-gate exits 1, happy path hits the right route with the right
// body. --json path is checked for one leaf per family to pin the
// shape. stdout-content assertions are deliberately absent (the
// load-bearing seam is the route + method; renderer drift is
// covered by the existing output_test.go family). Secrets plaintext
// / API-key plaintext / webhook secret plaintext are NEVER echoed
// by the CLI today (spec §17 G6) — those guards live in the leaf
// body itself, not in tests that could miss a future regression.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// fakeAPI spins up an httptest server that records the (method, path)
// of every request and returns the supplied JSON body. The handler
// can also assert the request body byte-shape (e.g. that
// Idempotency-Key was sent) by overriding with a custom handler.
type fakeAPI struct {
	t         *testing.T
	srv       *httptest.Server
	sawMethod string
	sawPath   string
	sawBody   []byte
	sawHeader http.Header
}

func newFakeAPI(t *testing.T, body string, status int) *fakeAPI {
	t.Helper()
	f := &fakeAPI{t: t}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.sawMethod = r.Method
		f.sawPath = r.URL.Path
		f.sawHeader = r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		f.sawBody = b
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.srv.Close)
	t.Setenv("FAAS_API", f.srv.URL)
	return f
}

// authedFakeAPI is newFakeAPI with FAAS_TOKEN already set. The
// token value matches the one t.Setenv injects so the SDK's
// "Authorization: Bearer ..." check is satisfied.
func authedFakeAPI(t *testing.T, body string, status int) *fakeAPI {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	return newFakeAPI(t, body, status)
}

// resetJSONOut flips the package-level jsonOutput back to false so
// leaves that consumed an --json token from applyJSONFlag don't
// bleed into siblings.
func resetJSONOut(t *testing.T) {
	t.Helper()
	orig := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = orig })
}

// --- webhooks rotate-secret ---

func TestTierB_WebhooksRotateSecret_NoAppFlagExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdWebhookRotateSecret(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierB_WebhooksRotateSecret_BadIDExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdWebhookRotateSecret([]string{"--app", "demo", "not-a-uuid"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (invalid id shape)", code)
	}
}

func TestTierB_WebhooksRotateSecret_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"rotated_at":"2026-08-07T12:00:00Z","webhook_secret_sealed_masked":"***"}`, http.StatusOK)
	if code := cmdWebhookRotateSecret([]string{"--app", "demo", "--secret", "known-replacement", "0123456789abcdef0123456789abcdef"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/apps/demo/webhooks/0123456789abcdef0123456789abcdef/rotate-secret" {
		t.Errorf("route = %s %s, want POST /v1/apps/demo/webhooks/.../rotate-secret", f.sawMethod, f.sawPath)
	}
	if !strings.Contains(string(f.sawBody), `"webhook_secret":"known-replacement"`) {
		t.Errorf("body = %s, want caller-supplied replacement", f.sawBody)
	}
}

// --- secrets list-all ---

func TestTierB_SecretsListAll_NoTokenExitsTwo(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("no network call expected without token")
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	// printErr returns 2 for auth-shaped errors (per CLI §3.2).
	if code := secretsListAll(nil); code != 2 {
		t.Fatalf("exit = %d, want 2 (auth)", code)
	}
}

func TestTierB_SecretsListAll_BadLimitExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := secretsListAll([]string{"--limit", "0"}); code != 1 {
		t.Fatalf("limit=0 exit = %d, want 1", code)
	}
	if code := secretsListAll([]string{"--limit", "500"}); code != 1 {
		t.Fatalf("limit=500 exit = %d, want 1", code)
	}
}

func TestTierB_SecretsListAll_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"secrets":[{"app_id":"a-1","app_slug":"demo","key":"FOO","ciphertext":"cipher","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-08-01T00:00:00Z"}],"next_before":"demo|FOO"}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := secretsListAll(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/secrets" {
		t.Errorf("route = %s %s, want GET /v1/secrets", f.sawMethod, f.sawPath)
	}
}

func TestTierB_SecretsListAll_EmptyEmitsParen(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"secrets":[],"next_before":""}`, http.StatusOK)
	if code := secretsListAll(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// --- audit-events get ---

func TestTierB_AuditEventsGet_HappyPath(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"42","at":"2026-08-07T00:00:00Z","actor":"apid","kind":"account.mfa.enroll","subject":"acct-1","data":{}}`, http.StatusOK)
	if code := cmdAuditEventsGet([]string{"42"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/audit-events/42" {
		t.Errorf("route = %s %s, want GET /v1/audit-events/42", f.sawMethod, f.sawPath)
	}
}

func TestTierB_AuditEventsGet_JSONShape(t *testing.T) {
	resetJSONOut(t)
	body := `{"id":"42","at":"2026-08-07T00:00:00Z","actor":"apid","kind":"k","subject":"s","data":{}}`
	authedFakeAPI(t, body, http.StatusOK)
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })
	if code := cmdAuditEventsGet([]string{"42"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// --- admin consume-credits ---

func TestTierB_AdminConsumeCredits_NoArgsExitsTwo(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdAdminConsumeCredits(nil); code != 2 {
		t.Fatalf("exit = %d, want 2 (operator error)", code)
	}
}

func TestTierB_AdminConsumeCredits_HappyPath(t *testing.T) {
	resetJSONOut(t)
	body := `{"invoice_id":"inv-1","consumed_cents":1000,"remaining_credits_cents":500,"already_consumed_for_invoice":false,"per_credit":[]}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdAdminConsumeCredits([]string{"inv-1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/invoices/inv-1/consume-credits" {
		t.Errorf("route = %s %s, want POST /v1/invoices/inv-1/consume-credits", f.sawMethod, f.sawPath)
	}
}

// --- overage-cap ---

func TestTierB_OverageCap_NoArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdOverageCap(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierB_OverageCap_BadArgExitsOne(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, "", http.StatusOK)
	if code := cmdOverageCap([]string{"abc"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierB_OverageCap_SetHappyPath(t *testing.T) {
	resetJSONOut(t)
	acct := api.AccountResponse{Email: "u@x", Plan: "hobby"}
	bodyBytes, _ := json.Marshal(acct)
	f := authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	if code := cmdOverageCap([]string{"500"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/account/overage-cap" {
		t.Errorf("route = %s %s, want POST /v1/account/overage-cap", f.sawMethod, f.sawPath)
	}
}

func TestTierB_OverageCap_ClearHappyPath(t *testing.T) {
	resetJSONOut(t)
	acct := api.AccountResponse{Email: "u@x", Plan: "hobby"}
	bodyBytes, _ := json.Marshal(acct)
	authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	// Real `--clear` flag (not the `-- --clear` positional workaround
	// — Go's flag.Parse eats any token starting with `-` before the
	// leaf sees it; see commit message for the regression this fixes).
	if code := cmdOverageCap([]string{"--clear"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// TestTierB_OverageCap_ClearWithCentsRejected pins that --clear and a
// positional <cents> are mutually exclusive (otherwise the caller
// can't tell whether they meant to set or clear).
func TestTierB_OverageCap_ClearWithCentsRejected(t *testing.T) {
	resetJSONOut(t)
	if code := cmdOverageCap([]string{"--clear", "500"}); code != 1 {
		t.Fatalf("exit = %d, want 1 (mutually exclusive)", code)
	}
}

// --- keys rotate ---

func TestTierB_KeysRotate_NoIDExitsOne(t *testing.T) {
	resetJSONOut(t)
	if code := cmdKeysRotate(nil); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTierB_KeysRotate_HappyPath(t *testing.T) {
	resetJSONOut(t)
	resp := api.RotateKeyResponse{
		Key:             api.APIKeyResponse{ID: "k-2", Label: "ci"},
		KeyPlaintext:    "new-secret-DO-NOT-LEAK",
		OldKeyID:        "k-1",
		OldKeyExpiresAt: "2026-08-14T00:00:00Z",
	}
	bodyBytes, _ := json.Marshal(resp)
	f := authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	if code := cmdKeysRotate([]string{"k-1"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "POST" || f.sawPath != "/v1/keys/k-1/rotate" {
		t.Errorf("route = %s %s, want POST /v1/keys/k-1/rotate", f.sawMethod, f.sawPath)
	}
}

// --- keys grace-window ---

func TestTierB_KeysGraceWindow_GetHappyPath(t *testing.T) {
	resetJSONOut(t)
	seven := 7
	resp := api.GraceWindowResponse{Days: &seven, PlanDefault: 7}
	bodyBytes, _ := json.Marshal(resp)
	f := authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	// No positional required — bare `gregale keys grace-window` reads.
	if code := cmdKeysGraceWindow(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/account/keys/grace_window_days" {
		t.Errorf("route = %s %s, want GET /v1/account/keys/grace_window_days", f.sawMethod, f.sawPath)
	}
}

func TestTierB_KeysGraceWindow_SetHappyPath(t *testing.T) {
	resetJSONOut(t)
	fourteen := 14
	resp := api.GraceWindowResponse{Days: &fourteen, PlanDefault: 7}
	bodyBytes, _ := json.Marshal(resp)
	authedFakeAPI(t, string(bodyBytes), http.StatusOK)
	if code := cmdKeysGraceWindow([]string{"--days", "14"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

// --- account dpa ---

func TestTierB_AccountDPA_NoTokenStillHitsRoute(t *testing.T) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "")
	f := newFakeAPI(t, "# DPA\n\nTerms here.\n", http.StatusOK)
	if code := cmdAccountDPA(nil); code != 0 {
		t.Fatalf("exit = %d, want 0 (DPA route is unauth)", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/account/dpa" {
		t.Errorf("route = %s %s, want GET /v1/account/dpa", f.sawMethod, f.sawPath)
	}
}

// --- orgs me ---

func TestTierB_OrgsMe_NoOrgInResponse(t *testing.T) {
	resetJSONOut(t)
	authedFakeAPI(t, `{"org":null}`, http.StatusOK)
	if code := cmdOrgsMe(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
}

func TestTierB_OrgsMe_OrgInResponse(t *testing.T) {
	resetJSONOut(t)
	body := `{"org":{"id":"o-1","slug":"acme","name":"ACME Co","personal":false,"plan":"scale","status":"active","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-08-07T00:00:00Z","role":"admin"}}`
	f := authedFakeAPI(t, body, http.StatusOK)
	if code := cmdOrgsMe(nil); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if f.sawMethod != "GET" || f.sawPath != "/v1/orgs/me" {
		t.Errorf("route = %s %s, want GET /v1/orgs/me", f.sawMethod, f.sawPath)
	}
}
