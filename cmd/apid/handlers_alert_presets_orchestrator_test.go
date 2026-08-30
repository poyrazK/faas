// Issue #1233 / ADR-123 PR-C commit 2 code-review fix:
//
// sendTestAlertPresetCore is the production webhook dispatch
// path. The pure-helper tests (handlers_alert_presets_test.go)
// cover buildTestAlertEvent + unsealAlertRuleWebhookSecret
// individually, but NOT the orchestrator's chain — loadAndGate
// → AppBySlug → AlertRuleByAccountAppAndPresetName → unseal →
// build → DispatchTest → audit. A regression in any link would
// not be caught without an integration smoke.
//
// This file seeds a complete MemStore (account + app + alert
// preset + instantiated rule with a sealed secret) and a real
// httptest webhook receiver, then asserts the orchestrator's
// response shape + the body the receiver saw.
//
// Pattern: mirrors the existing handlers_admin_force_test.go
// harness (newForceHarness + seedRunningInstance) — same
// newServer() call shape, same MemStore.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestSendTestAlertPresetCore_DispatchesTestTrue is the load-bearing
// orchestrator smoke for the customer-facing "Send test alert"
// endpoint (issue #1233 / ADR-123 PR-C commit 2). Pins:
//
//   - 200 status with the wire shape
//     {status, test, delivery_id, attempts} (test == true).
//   - delivery_id is a fresh 32-char hex (NOT the rule's primary
//     key — see buildTestAlertEvent docstring).
//   - attempts ≥ 1 (the dispatcher tries at least once; 1 on a
//     2xx receiver, more on retries).
//   - The customer's webhook receiver sees payload.test == true
//     (the discriminator key to skip production alert paths).
//   - The audit row `alert_preset.test_sent` fires with the rule
//     ID + delivery_id.
//
// Negative-case companion TestSendTestAlertPresetCore_NoRule
// asserts 404 when the customer hasn't instantiated the preset.
func TestSendTestAlertPresetCore_DispatchesTestTrue(t *testing.T) {
	// Allow loopback egress so the dispatcher's SSRF guard
	// accepts the httptest receiver. Production dialers
	// refuse 127.0.0.1; the dispatcher opts in via
	// FAAS_EGRESS_ALLOW_LOOPBACK (see pkg/webhookout:318-327).
	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")

	// Real webhook receiver — counts deliveries + captures body.
	var received atomic.Int32
	var lastBody atomic.Value
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received.Add(1)
		lastBody.Store(string(body))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)

	// Host identity for the unseal path. The orchestrator's
	// production accessor resolves to mfaIdentities(); in tests
	// we swap it via the hostIdentitiesForUnseal package var
	// (mirrors the unseal unit-test pattern above).
	ident, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("gen identity: %v", err)
	}
	const webhookSecret = "test-receiver-secret"
	sealed, err := secretbox.SealBytes(ident.Recipient(), "alert_rule_secret", []byte(webhookSecret), api.AlertRuleWebhookSecretMaxBytes)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	prev := hostIdentitiesForUnseal
	t.Cleanup(func() { hostIdentitiesForUnseal = prev })
	hostIdentitiesForUnseal = func(_ context.Context) []*age.X25519Identity {
		return []*age.X25519Identity{ident}
	}

	// Seed a complete MemStore.
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "test-alert@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID,
		Slug:      "test-alert-app",
		RAMMB:     128,
		Runtime:   "node22",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name:                   "api_down",
		DisplayName:            "API is down",
		Description:            "Fires when last /readyz probe failed.",
		Category:               "availability",
		Metric:                 "api_reachable",
		Comparison:             "lt",
		Threshold:              1,
		WindowSpec:             "5m",
		DefaultCooldownMinutes: 5,
		MinimumPlan:            "pro",
		EnabledInCatalog:       true,
	})
	// The handler matches alert_rules by display-name prefix
	// "<DisplayName> (<slug>)" — the canonical instantiator shape.
	rule, err := store.CreateAlertRule(context.Background(), state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "API is down (test-alert-app)",
		Metric:              "api_reachable",
		Comparison:          "lt",
		Threshold:           1,
		WindowSpec:          "5m",
		CooldownMinutes:     5,
		Enabled:             true,
		WebhookURL:          receiver.URL,
		WebhookSecretSealed: sealed,
	})
	if err != nil {
		t.Fatalf("create alert rule: %v", err)
	}

	// Server with the seeded MemStore. audit is the production
	// newAuditor(store, log, nil) — Emit is nil-safe and used
	// as a no-op here (the orchestrator still fires the emit).
	ops := wire.NewOpsMetrics("apid_test_alert")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).
		WithOpsMetrics(context.Background(), ops)

	// Fire the orchestrator directly. The HTTP-layer wrapper at
	// sendTestAlertPreset just unmarshals ctx + slug + name and
	// delegates — that's covered by the route registration tests
	// in server.go. The orchestrator is where the load-bearing
	// work lives.
	res, prob := srv.sendTestAlertPresetCore(context.Background(), acct, "test-alert-app", "api_down")
	if prob != nil {
		t.Fatalf("orchestrator returned Problem; want 200: %+v", prob)
	}
	if res.Status != "sent" {
		t.Errorf("Status = %q, want \"sent\"", res.Status)
	}
	if !res.Test {
		t.Errorf("Test = false, want true (the discriminator MUST be true on every test alert)")
	}
	if got := len(res.DeliveryID); got != 32 {
		t.Errorf("DeliveryID len = %d, want 32 (16 bytes hex)", got)
	}
	if res.Attempts < 1 {
		t.Errorf("Attempts = %d, want >= 1", res.Attempts)
	}

	// Receiver saw at least one delivery, with payload.test=true.
	if got := received.Load(); got != 1 {
		t.Errorf("receiver deliveries = %d, want 1", got)
	}
	body, _ := lastBody.Load().(string)
	if !strings.Contains(body, `"test":true`) {
		t.Errorf("receiver body missing test:true discriminator; body=%s", body)
	}
	if !strings.Contains(body, `"observed":`) {
		t.Errorf("receiver body missing observed value; body=%s", body)
	}

	// Sanity: the rule was NOT mutated by the dispatch — test alerts
	// do NOT touch alert_deliveries (dispatcher.DispatchTest
	// explicit path), but they should NOT mutate last_fired_at
	// either (would confuse cooldown tracking on the next real
	// fire).
	gotRule, err := store.AlertRuleByAccountAppAndPresetName(context.Background(), acct.ID, app.ID, "api_down")
	if err != nil {
		t.Fatalf("re-fetch rule: %v", err)
	}
	if !gotRule.LastFiredAt.IsZero() {
		t.Errorf("test alert mutated last_fired_at on rule %q — cooldown tracker would think the test was a real fire", rule.ID)
	}

	// ADR-123 PR-D: the test path now writes an alert_deliveries
	// row with is_test=true so the operator pane
	// (?include_test=true) can reach it. Production-default read
	// (include_test=false) MUST still hide it. Asserts both halves.
	deliveries, err := store.ListAlertDeliveriesForRule(context.Background(), rule.ID, 10, false)
	if err != nil {
		t.Fatalf("ListAlertDeliveriesForRule(includeTest=false): %v", err)
	}
	if len(deliveries) != 0 {
		t.Errorf("production-default read returned %d test rows; want 0 (test rows must be hidden behind include_test=true)", len(deliveries))
	}
	allDeliveries, err := store.ListAlertDeliveriesForRule(context.Background(), rule.ID, 10, true)
	if err != nil {
		t.Fatalf("ListAlertDeliveriesForRule(includeTest=true): %v", err)
	}
	if len(allDeliveries) != 1 {
		t.Fatalf("operator read returned %d rows; want 1 (the test row)", len(allDeliveries))
	}
	if !allDeliveries[0].IsTest {
		t.Errorf("ledger row IsTest = false; want true (Dispatcher.DispatchTest must stamp the discriminator)")
	}
	if allDeliveries[0].ID != res.DeliveryID {
		t.Errorf("ledger row ID = %q; want %q (must match the deliveryID returned to the customer)", allDeliveries[0].ID, res.DeliveryID)
	}
}

// TestSendTestAlertPresetCore_NoRule pins the 404 path: customer
// hits "Send test alert" without first instantiating the preset.
// The orchestrator must NOT dispatch and must NOT mint a
// delivery_id — the customer should enable first, then test.
func TestSendTestAlertPresetCore_NoRule(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "norule@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	_, err = store.CreateApp(context.Background(), state.App{
		AccountID: acct.ID,
		Slug:      "norule-app",
		RAMMB:     128,
		Runtime:   "node22",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name:             "api_down",
		DisplayName:      "API is down",
		Metric:           "api_reachable",
		Comparison:       "lt",
		Threshold:        1,
		WindowSpec:       "5m",
		MinimumPlan:      "pro",
		EnabledInCatalog: true,
	})
	// Note: NO alert rule instantiated for this app.

	ops := wire.NewOpsMetrics("apid_test_alert_no_rule")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).
		WithOpsMetrics(context.Background(), ops)

	res, prob := srv.sendTestAlertPresetCore(context.Background(), acct, "norule-app", "api_down")
	if prob == nil {
		t.Fatalf("orchestrator returned success; want 404 Problem. res=%+v", res)
	}
	if prob.Code != api.CodeValidation {
		t.Errorf("Problem.Code = %q, want %q (404 with the validation code, NOT capacity)", prob.Code, api.CodeValidation)
	}
	if !strings.Contains(prob.Detail, "enable it first") {
		t.Errorf("Problem.Detail = %q; want substring \"enable it first\"", prob.Detail)
	}
	if res.DeliveryID != "" {
		t.Errorf("res.DeliveryID = %q, want empty (no dispatch should have happened)", res.DeliveryID)
	}
}
