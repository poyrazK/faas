// MemStore coverage tests for the two methods added in
// ADR-123 PR-C commit 2 ("Send test alert" button):
//   - AlertRuleByAccountAppAndPresetName (mirrors the pgstore's
//     display-name-prefix lookup)
//   - SeedAlertPresetForTest (test-only seeder used by the
//     orchestrator end-to-end smoke in
//     cmd/apid/handlers_alert_presets_orchestrator_test.go)
//
// pkg/state's coverage floor is 70% (Makefile:184). These tests
// exist primarily to keep the new MemStore statements covered
// when the orchestrator test runs under no_pg; without them the
// floor drops below 70% on CI.
package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestMemStore_AlertRuleByAccountAppAndPresetName pins the
// display-name prefix lookup. The catalog row's DisplayName +
// " (" is the canonical prefix the enable path uses when
// stamping alert_rules.name (see handlers_alert_presets.go:255).
func TestMemStore_AlertRuleByAccountAppAndPresetName(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "preset@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID,
		Slug:      "preset-app",
		RAMMB:     128,
		Runtime:   "node22",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name:        "api_down",
		DisplayName: "API is down",
		Metric:      "api_reachable",
		Comparison:  "lt",
		Threshold:   1,
		WindowSpec:  "5m",
		MinimumPlan: "pro",
	})
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID:       acct.ID,
		AppID:           app.ID,
		Name:            "API is down (preset-app)",
		Metric:          "api_reachable",
		Comparison:      "lt",
		Threshold:       1,
		WindowSpec:      "5m",
		CooldownMinutes: 5,
		Enabled:         true,
		WebhookURL:      "https://example.invalid/hook",
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}

	// Happy path: matches by prefix.
	got, err := store.AlertRuleByAccountAppAndPresetName(ctx, acct.ID, app.ID, "api_down")
	if err != nil {
		t.Fatalf("lookup happy: %v", err)
	}
	if got.ID != rule.ID {
		t.Errorf("ID = %q, want %q", got.ID, rule.ID)
	}

	// No preset in catalog → ErrNotFound.
	if _, err := store.AlertRuleByAccountAppAndPresetName(ctx, acct.ID, app.ID, "missing_preset"); err == nil {
		t.Errorf("expected ErrNotFound for missing preset; got nil")
	}

	// Preset exists but no rule instantiated for this app →
	// ErrNotFound.
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name:        "deploy_failed",
		DisplayName: "Deployment failed",
		Metric:      "apid_deployment_failed_total",
		Comparison:  "gt",
		Threshold:   0,
		WindowSpec:  "1h",
		MinimumPlan: "hobby",
	})
	if _, err := store.AlertRuleByAccountAppAndPresetName(ctx, acct.ID, app.ID, "deploy_failed"); err == nil {
		t.Errorf("expected ErrNotFound when no rule matches; got nil")
	}
}
