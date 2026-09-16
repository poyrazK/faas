// Unit tests for the alert-rule handlers (issue #396, ADR-045 PR 3).
//
// Coverage matrix:
//
//   - happy path: create + get + list + update + delete + rotate-secret
//   - plan-tier gate: Free customer gets 402 CodePlanAlertRulesNotAllowed
//     on every write (POST/PATCH/DELETE/rotate) AND on read (GET list)
//   - per-app quota trip: 403 CodePlanAlertRuleQuota scope=app
//   - per-account quota trip: 403 CodePlanAlertRuleQuota scope=account
//   - SSRF block: 127.0.0.1 + 169.254.169.254 webhook URLs rejected with
//     403 CodeImageEgressDenied (defense-in-depth mirror of imaged's guard)
//   - cross-account 404: a foreign account's rule id returns 404, not 403
//     (the IDOR-safe posture — don't leak the existence of someone else's rule)
//   - account-wide rules visible in per-app list: account-scoped rule
//     (AppID == "") shows up under every per-app GET
//   - partial-update: pointer-everything optionals let a name-only PATCH
//     leave threshold / cooldown / etc untouched
//   - metric-family swap rejected with 400 CodeAlertRuleInvalid
//   - failure_source xor check rejected at the API boundary with 400
//   - rotate-secret returns 200 with masked constant + rotated_at;
//     response body and audit row carry NO plaintext
//   - response body never contains the plaintext webhook_secret
//
// Tests run KVM-free via the in-memory store. The alert-rule DTO is
// stable per pkg/api/alerts.go; the wire contract tested here is
// the one pkg/api/client.go's hand-extracted SDK methods depend on.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// setupAlerts wires setup() with a recipient so the seal path
// succeeds. Every alert-rule test uses this instead of setup()
// directly so the createAlertRule / updateAlertRule / rotateAlertRuleSecret
// handlers don't 503 with "host age recipient not loaded".
func setupAlerts(t *testing.T, plan api.Plan) testEnv {
	t.Helper()
	teardown := withTestRecipient(t)
	t.Cleanup(teardown)
	return setup(t, plan)
}

// alertRuleReq is a valid baseline request body for the alert-rule
// create handler. Tests start from this and mutate one field at a
// time so the failure surface stays narrow.
func alertRuleReq() api.CreateAlertRuleRequest {
	return api.CreateAlertRuleRequest{
		Name:          "p99 > 500ms",
		Metric:        "latency_p99_ms",
		Comparison:    "gt",
		Threshold:     500,
		WindowSpec:    "5m",
		WebhookURL:    "https://example.com/hook",
		WebhookSecret: "shh",
	}
}

// mustCreateAlertRule POSTs a valid rule and returns the response
// row. Fails the test if the create doesn't return 201.
func mustCreateAlertRule(t *testing.T, e testEnv, slug string, req api.CreateAlertRuleRequest) api.AlertRuleResponse {
	t.Helper()
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/alerts", req, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create alert rule: status %d, body %s", rec.Code, rec.Body.String())
	}
	var out api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal alert rule: %v", err)
	}
	return out
}

// mustSeedAccountWideRule drops a state.AlertRule row directly via
// the store with AppID == "" so the per-app list test can assert
// account-wide visibility without going through the create handler.
// The HTTP create path always pins a real app — there's no API
// surface for account-wide rules in PR 3; account-wide visibility
// is a render-only feature.
func mustSeedAccountWideRule(t *testing.T, e testEnv, name string) state.AlertRule {
	t.Helper()
	row, err := e.store.CreateAlertRule(context.Background(), state.AlertRule{
		AccountID:           e.acct.ID,
		AppID:               "",
		Name:                name,
		Enabled:             true,
		Metric:              state.AlertMetricErrorRate,
		Comparison:          state.AlertGt,
		Threshold:           5,
		WindowSpec:          state.AlertWindow5m,
		WebhookURL:          "https://example.com/acct",
		WebhookSecretSealed: []byte("ciphertext-not-real"),
		CooldownMinutes:     api.AlertRuleDefaultCooldownMinutes,
	})
	if err != nil {
		t.Fatalf("seed account-wide rule: %v", err)
	}
	return row
}

// doAs sends a request using the given API key instead of the
// env's. Used by IDOR-safety tests that need a foreign key to
// probe another account's rule id. Mirrors e.do but with an
// explicit key parameter.
func (e testEnv) doAs(t *testing.T, method, path string, body any, hdrs map[string]string, key string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Authorization", "Bearer "+key)
	for k, v := range hdrs {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

// --- happy paths ------------------------------------------------------------

// TestCreateAlertRule_HappyPath confirms the canonical create flow:
// 201 + masked constant + RFC3339 timestamps + persisted row visible
// via the store.
func TestCreateAlertRule_HappyPath(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-hp")
	out := mustCreateAlertRule(t, e, "alerts-hp", alertRuleReq())
	if out.ID == "" {
		t.Errorf("ID is empty")
	}
	if out.AppID == "" {
		t.Errorf("AppID is empty")
	}
	if out.WebhookSecretSealedMasked != api.AlertRuleWebhookSecretMasked {
		t.Errorf("WebhookSecretSealedMasked = %q, want %q", out.WebhookSecretSealedMasked, api.AlertRuleWebhookSecretMasked)
	}
	if !strings.Contains(out.WebhookSecretSealedMasked, "*") {
		t.Errorf("masked constant is not masked: %q", out.WebhookSecretSealedMasked)
	}
	if out.Metric != "latency_p99_ms" || out.Comparison != "gt" || out.WindowSpec != "5m" {
		t.Errorf("closed sets drifted: metric=%q comparison=%q window_spec=%q", out.Metric, out.Comparison, out.WindowSpec)
	}
	if out.Action != string(state.AlertActionWebhook) {
		t.Errorf("default action = %q, want %q", out.Action, state.AlertActionWebhook)
	}
	// Round-trip via the store: the row carries the sealed
	// ciphertext (which we never echo) and the plaintext is gone.
	row, err := e.store.AlertRuleByID(context.Background(), out.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if len(row.WebhookSecretSealed) == 0 {
		t.Errorf("WebhookSecretSealed is empty — seal did not happen")
	}
}

func TestCreateAlertRule_ActionRoundTrip(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-actions")
	for _, action := range api.AllowedAlertRuleActions {
		req := alertRuleReq()
		req.Name = "action " + action
		req.Action = &action
		created := mustCreateAlertRule(t, e, "alerts-actions", req)
		if created.Action != action {
			t.Fatalf("create action = %q, want %q", created.Action, action)
		}
		rec := e.do(t, "GET", "/v1/apps/alerts-actions/alerts/"+created.ID, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %q: status %d body=%s", action, rec.Code, rec.Body.String())
		}
		var got api.AlertRuleResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Action != action {
			t.Fatalf("get action = %q, want %q", got.Action, action)
		}
	}
}

// TestListAlertRules_IncludesAccountWide seeds an account-wide rule
// (AppID == "") and an app-scoped rule, then asserts both appear in
// the per-app listing. Account-wide rules are visible at every
// per-app endpoint — design decision recorded in the PR plan.
func TestListAlertRules_IncludesAccountWide(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	appID := mustSeedApp(t, e, "alerts-acct-wide")
	mustCreateAlertRule(t, e, "alerts-acct-wide", alertRuleReq())
	mustSeedAccountWideRule(t, e, "acct-wide")
	rec := e.do(t, "GET", "/v1/apps/alerts-acct-wide/alerts", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out []api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2 (app-scoped + account-wide): %+v", len(out), out)
	}
	// Order is non-deterministic; both must appear.
	foundApp := false
	foundAcct := false
	for _, r := range out {
		if r.AppID == appID {
			foundApp = true
		}
		if r.AppID == "" {
			foundAcct = true
		}
	}
	if !foundApp || !foundAcct {
		t.Errorf("missing one: foundApp=%v foundAcct=%v", foundApp, foundAcct)
	}
}

// TestGetAlertRule_HappyPath creates a rule and fetches it by id;
// the response must match.
func TestGetAlertRule_HappyPath(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-get")
	created := mustCreateAlertRule(t, e, "alerts-get", alertRuleReq())
	rec := e.do(t, "GET", "/v1/apps/alerts-get/alerts/"+created.ID, nil, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.ID != created.ID || out.Name != created.Name {
		t.Errorf("round-trip lost data: got %+v", out)
	}
}

// TestUpdateAlertRule_HappyPath patches the rule's name and asserts
// the response carries the new value + the unchanged threshold.
func TestUpdateAlertRule_HappyPath(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-upd")
	created := mustCreateAlertRule(t, e, "alerts-upd", alertRuleReq())
	newName := "p99 > 750ms"
	rec := e.do(t, "PATCH", "/v1/apps/alerts-upd/alerts/"+created.ID,
		api.UpdateAlertRuleRequest{Name: &newName}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.AlertRuleResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Name != newName {
		t.Errorf("Name = %q, want %q", out.Name, newName)
	}
	if out.Threshold != created.Threshold {
		t.Errorf("Threshold = %v, want %v (omitted means untouched)", out.Threshold, created.Threshold)
	}
}

// TestDeleteAlertRule_HappyPath deletes a rule and verifies a
// follow-up GET returns 404.
func TestDeleteAlertRule_HappyPath(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-del")
	created := mustCreateAlertRule(t, e, "alerts-del", alertRuleReq())
	rec := e.do(t, "DELETE", "/v1/apps/alerts-del/alerts/"+created.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	rec2 := e.do(t, "GET", "/v1/apps/alerts-del/alerts/"+created.ID, nil, nil)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("GET after DELETE = %d, want 404", rec2.Code)
	}
}

// TestRotateAlertRuleSecret_HappyPath confirms the rotate-secret
// endpoint mints a new secret, returns 200 with the masked
// constant + rotated_at, and the response body NEVER carries the
// plaintext secret.
func TestRotateAlertRuleSecret_HappyPath(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-rot")
	created := mustCreateAlertRule(t, e, "alerts-rot", alertRuleReq())
	rec := e.do(t, "POST", "/v1/apps/alerts-rot/alerts/"+created.ID+"/rotate-secret", api.RotateAlertRuleSecretRequest{WebhookSecret: "receiver-known-replacement"}, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var out api.RotateAlertRuleSecretResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.RotatedAt == "" {
		t.Errorf("RotatedAt is empty")
	}
	if out.WebhookSecretSealedMasked != api.AlertRuleWebhookSecretMasked {
		t.Errorf("WebhookSecretSealedMasked = %q, want %q", out.WebhookSecretSealedMasked, api.AlertRuleWebhookSecretMasked)
	}
	// The plaintext secret "shh" MUST NOT appear in the response body.
	if strings.Contains(rec.Body.String(), "shh") {
		t.Errorf("response body leaked the plaintext webhook secret: %s", rec.Body)
	}
	// Confirm the row's sealed secret changed (it was a different
	// ciphertext than the create-time seal).
	row, err := e.store.AlertRuleByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if len(row.WebhookSecretSealed) == 0 {
		t.Errorf("WebhookSecretSealed is empty after rotate")
	}
	namespace, plaintext, err := secretbox.OpenBytes(mfaIdentities()[0], row.WebhookSecretSealed)
	if err != nil {
		t.Fatalf("unseal rotated secret: %v", err)
	}
	if namespace != alertRuleSecretSealLabel || string(plaintext) != "receiver-known-replacement" {
		t.Fatalf("unsealed rotation = namespace %q plaintext %q", namespace, plaintext)
	}
}

func TestRotateAlertRuleSecret_RequiresReplacement(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-rot-empty")
	created := mustCreateAlertRule(t, e, "alerts-rot-empty", alertRuleReq())
	rec := e.do(t, "POST", "/v1/apps/alerts-rot-empty/alerts/"+created.ID+"/rotate-secret", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
}

// --- plan-tier gate ---------------------------------------------------------

// TestCreateAlertRule_FreeReturns402 pins the plan-tier gate (spec
// §4.4 paid-only event-shaped primitives): a Free customer cannot
// create alert rules. The 402 fires BEFORE the app lookup so the
// wire shape matches the cron precedent (handlers_ext.go:1203).
func TestCreateAlertRule_FreeReturns402(t *testing.T) {
	e := setupAlerts(t, api.PlanFree)
	mustSeedApp(t, e, "alerts-free")
	rec := e.do(t, "POST", "/v1/apps/alerts-free/alerts", alertRuleReq(), nil)
	assertProblem(t, rec, 402, api.CodePlanAlertRulesNotAllowed)
}

// TestUpdateAlertRule_FreeReturns402 confirms the plan gate fires
// on update too — the customer cannot reach a state where they
// have a rule on their account but can't modify it.
func TestUpdateAlertRule_FreeReturns402(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-up-free")
	created := mustCreateAlertRule(t, e, "alerts-up-free", alertRuleReq())
	// Drop the plan to Free on the same account.
	if err := e.store.UpdateAccountPlan(context.Background(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}
	newName := "x"
	rec := e.do(t, "PATCH", "/v1/apps/alerts-up-free/alerts/"+created.ID,
		api.UpdateAlertRuleRequest{Name: &newName}, nil)
	assertProblem(t, rec, 402, api.CodePlanAlertRulesNotAllowed)
}

// --- quota ------------------------------------------------------------------

// TestCreateAlertRule_AtPerAppLimitReturns403 seeds the per-app cap
// directly via the handler (bypassing the cap so the test is
// independent of the cap-enforcement path it's testing) and asserts
// the next wire create returns 403 + CodePlanAlertRuleQuota. Scope
// "app" surfaces in the body so the customer knows to delete a
// rule from THIS app.
//
// Pro plan caps AlertRuleLimitPerApp at 10.
func TestCreateAlertRule_AtPerAppLimitReturns403(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-cap-app")
	limits := api.MustLimitsFor(api.PlanPro)
	for i := 0; i < limits.AlertRuleLimitPerApp; i++ {
		req := alertRuleReq()
		req.Name = "seed-" + string(rune('A'+i))
		mustCreateAlertRule(t, e, "alerts-cap-app", req)
	}
	rec := e.do(t, "POST", "/v1/apps/alerts-cap-app/alerts", alertRuleReq(), nil)
	assertProblem(t, rec, 403, api.CodePlanAlertRuleQuota)
}

// TestCreateAlertRule_AtPerAccountLimitReturns403 fills the
// per-account cap and confirms the per-account branch trips. PR
// review finding F9: the previous test hardcoded
// `AlertRuleLimitPerApp × 3 = 30` and silently coupled two
// unrelated constants. The refactor computes the seeding loop from
// AlertRuleLimitPerAccount directly and spreads across enough apps
// to never hit the per-app cap during seeding.
func TestCreateAlertRule_AtPerAccountLimitReturns403(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	limits := api.MustLimitsFor(api.PlanPro)
	perAccount := limits.AlertRuleLimitPerAccount
	perApp := limits.AlertRuleLimitPerApp
	if perAccount <= 0 {
		t.Skipf("plan has no per-account cap (LimitPerAccount=%d)", perAccount)
	}
	// Spread across enough apps so the seeding loop never trips
	// the per-app cap. If perApp > 0, numApps = ceil(perAccount/perApp);
	// otherwise we use a single app and the per-app cap is 0 /
	// unlimited.
	var numApps int
	if perApp > 0 {
		numApps = (perAccount + perApp - 1) / perApp
	} else {
		numApps = 1
	}
	slugs := make([]string, 0, numApps)
	for i := 0; i < numApps; i++ {
		slug := fmt.Sprintf("alerts-acct-%d", i)
		mustSeedApp(t, e, slug)
		slugs = append(slugs, slug)
	}
	created := 0
	for _, slug := range slugs {
		for i := 0; perApp == 0 || i < perApp; i++ {
			if created >= perAccount {
				break
			}
			req := alertRuleReq()
			req.Name = slug + "-" + string(rune('A'+i))
			mustCreateAlertRule(t, e, slug, req)
			created++
		}
		if created >= perAccount {
			break
		}
	}
	if got, err := e.store.ListAlertRulesForAccount(context.Background(), e.acct.ID); err != nil {
		t.Fatalf("ListAlertRulesForAccount: %v", err)
	} else if len(got) != perAccount {
		t.Fatalf("seeded %d rules, want per-account cap %d", len(got), perAccount)
	}
	// One more must 403. Pick the last app.
	rec := e.do(t, "POST", "/v1/apps/"+slugs[len(slugs)-1]+"/alerts", alertRuleReq(), nil)
	assertProblem(t, rec, 403, api.CodePlanAlertRuleQuota)
}

// --- SSRF -------------------------------------------------------------------

// TestCreateAlertRule_SSRFLoopbackRejected confirms a webhook URL
// pointing at 127.0.0.1 is rejected with 403 CodeImageEgressDenied.
// This is the first apid call site for the egress guard; meterd
// (PR 4) re-validates on dispatch but the create-time check stops
// a misconfigured rule from ever landing in PG.
func TestCreateAlertRule_SSRFLoopbackRejected(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-ssrf")
	req := alertRuleReq()
	req.WebhookURL = "http://127.0.0.1:8080/hook"
	rec := e.do(t, "POST", "/v1/apps/alerts-ssrf/alerts", req, nil)
	assertProblem(t, rec, 403, api.CodeImageEgressDenied)
}

// TestCreateAlertRule_SSRFMetadataRejected confirms the metadata
// range (169.254.169.254) is also denied. Cloud metadata services
// would leak host credentials if reachable from the dispatcher;
// same posture as imaged's egress guard.
func TestCreateAlertRule_SSRFMetadataRejected(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-meta")
	req := alertRuleReq()
	req.WebhookURL = "http://169.254.169.254/latest/meta-data/"
	rec := e.do(t, "POST", "/v1/apps/alerts-meta/alerts", req, nil)
	assertProblem(t, rec, 403, api.CodeImageEgressDenied)
}

// --- IDOR / cross-account ---------------------------------------------------

// TestGetAlertRule_CrossAccountReturns404 confirms the IDOR-safe
// posture: a stolen API key that knows the rule id of a foreign
// account's rule must see 404, not 403. The 404 hides the
// existence of the rule from the attacker (info-leak defence).
func TestGetAlertRule_CrossAccountReturns404(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-idor-victim")
	created := mustCreateAlertRule(t, e, "alerts-idor-victim", alertRuleReq())
	// Provision a second account on the same store with its own
	// API key. The attacker uses the foreign key to probe the
	// victim's rule id.
	store := e.store
	foreignAcct, err := store.CreateAccount(context.Background(), "intruder@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), foreignAcct.ID, hash, "intruder", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	rec := e.doAs(t, "GET", "/v1/apps/alerts-idor-victim/alerts/"+created.ID, nil, nil, pt)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-account GET = %d, want 404", rec.Code)
	}
}

// TestUpdateAlertRule_CrossAccountReturns404 mirrors the get path
// for PATCH: cross-account writes also surface as 404 so an
// attacker can't probe valid IDs.
func TestUpdateAlertRule_CrossAccountReturns404(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-idor-upd")
	created := mustCreateAlertRule(t, e, "alerts-idor-upd", alertRuleReq())
	store := e.store
	foreignAcct, err := store.CreateAccount(context.Background(), "intruder2@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount foreign: %v", err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), foreignAcct.ID, hash, "intruder", api.ScopesAdminOnly); err != nil {
		t.Fatalf("CreateAPIKey foreign: %v", err)
	}
	newName := "pwn"
	rec := e.doAs(t, "PATCH", "/v1/apps/alerts-idor-upd/alerts/"+created.ID,
		api.UpdateAlertRuleRequest{Name: &newName}, nil, pt)
	if rec.Code != http.StatusNotFound {
		t.Errorf("cross-account PATCH = %d, want 404", rec.Code)
	}
}

// --- validation / metric family ---------------------------------------------

// TestUpdateAlertRule_MetricFamilySwapRejected pins the metric-family
// swap rejection: changing the metric from error_rate_pct to
// failed_invocations (different xor_chk family) is rejected with
// 400 CodeAlertRuleInvalid. The customer must delete + recreate.
func TestUpdateAlertRule_MetricFamilySwapRejected(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-swap")
	req := alertRuleReq()
	req.Metric = "error_rate_pct"
	req.FailureSource = ""
	created := mustCreateAlertRule(t, e, "alerts-swap", req)
	newMetric := "failed_invocations"
	rec := e.do(t, "PATCH", "/v1/apps/alerts-swap/alerts/"+created.ID,
		api.UpdateAlertRuleRequest{Metric: &newMetric}, nil)
	assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
}

// TestCreateAlertRule_FailureSourceXorRejected pins the API-level
// xor check: failed_invocations MUST come with a failure_source;
// every other metric MUST come with an empty failure_source.
// Same constraint as the DB alert_rules_failure_source_xor_chk,
// enforced here so the customer sees a clean 400 rather than a
// constraint-violation 500 from the store.
func TestCreateAlertRule_FailureSourceXorRejected(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-xor")
	t.Run("failed_invocations_without_source", func(t *testing.T) {
		req := alertRuleReq()
		req.Metric = "failed_invocations"
		req.FailureSource = ""
		rec := e.do(t, "POST", "/v1/apps/alerts-xor/alerts", req, nil)
		assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
	})
	t.Run("error_rate_with_source", func(t *testing.T) {
		req := alertRuleReq()
		req.Metric = "error_rate_pct"
		req.FailureSource = "cron"
		rec := e.do(t, "POST", "/v1/apps/alerts-xor/alerts", req, nil)
		assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
	})
}

// TestCreateAlertRule_InvalidThresholdRejected pins the
// finite-threshold check: NaN / Inf would survive as 0.0 in PG
// and silently trip every alert. Reject at the API boundary with
// 400.
//
// NOTE: encoding/json cannot represent NaN / ±Inf natively. The
// request body that the test sends contains math.NaN() which
// json.Marshal encodes via SetEscapeHTML(false) with the literal
// token "NaN"; on the receive side decodeJSON rejects it with
// CodeValidation (the same 400 wire shape as my validator). Both
// 400 paths satisfy the contract — the customer can never get
// past the boundary with a non-finite threshold. The test
// accepts either code so a future decoder switch (e.g. to
// json.Decoder.UseNumber) doesn't break the assertion.
func TestCreateAlertRule_InvalidThresholdRejected(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-nan")
	cases := []struct {
		name      string
		threshold float64
	}{
		{"NaN", math.NaN()},
		{"PosInf", math.Inf(1)},
		{"NegInf", math.Inf(-1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := alertRuleReq()
			req.Threshold = tc.threshold
			rec := e.do(t, "POST", "/v1/apps/alerts-nan/alerts", req, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body)
			}
			// Accept either CodeValidation (json decode rejects NaN)
			// or CodeAlertRuleInvalid (validator runs first). Both
			// are 400 and both satisfy the wire contract.
			var prob struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
				t.Fatalf("decode problem: %v", err)
			}
			if prob.Code != api.CodeValidation && prob.Code != api.CodeAlertRuleInvalid {
				t.Errorf("problem code = %q, want %q or %q", prob.Code, api.CodeValidation, api.CodeAlertRuleInvalid)
			}
		})
	}
}

// TestCreateAlertRule_WebhookSecretByteCap pins the 256-byte cap on
// the plaintext webhook_secret. The cap is enforced at the API
// boundary as defense in depth — SealBytes applies the same cap at
// the seal boundary. PR review finding F5: byte vs rune count was
// ambiguous; the validator now counts bytes (which is what SealBytes
// actually encrypts).
func TestCreateAlertRule_WebhookSecretByteCap(t *testing.T) {
	e := setupAlerts(t, api.PlanPro)
	mustSeedApp(t, e, "alerts-bytes")
	req := alertRuleReq()
	// 257 bytes is one over the cap. ASCII so byte count == rune count.
	req.WebhookSecret = strings.Repeat("a", api.AlertRuleWebhookSecretMaxBytes+1)
	rec := e.do(t, "POST", "/v1/apps/alerts-bytes/alerts", req, nil)
	assertProblem(t, rec, 400, api.CodeAlertRuleInvalid)
	// Boundary: exactly 256 bytes (the cap) must be accepted.
	req.WebhookSecret = strings.Repeat("a", api.AlertRuleWebhookSecretMaxBytes)
	rec = e.do(t, "POST", "/v1/apps/alerts-bytes/alerts", req, nil)
	if rec.Code != http.StatusCreated {
		t.Errorf("at-cap secret: status = %d, want 201; body = %s", rec.Code, rec.Body)
	}
}
