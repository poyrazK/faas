package state_test

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMCPProtocolAlertStoreRoundTrip(t *testing.T) {
	check := func(t *testing.T, s state.Store) {
		t.Helper()
		ctx := context.Background()
		acct, err := s.CreateAccount(ctx, "customer-alert@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "customer-alert-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreateAlertRule(ctx, state.AlertRule{AccountID: acct.ID, AppID: app.ID, Name: "RPC failures", Metric: state.AlertMetric(api.MetricMCPProtocolErrorRate), Comparison: state.AlertGt, Threshold: 10, WindowSpec: "5m", Action: state.AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed-fixture"), CooldownMinutes: 15, Enabled: true})
		if err != nil || rule.MCPMinCalls != 20 || rule.MCPForMinutes != 10 {
			t.Fatalf("create: %+v %v", rule, err)
		}
		calls, duration := 40, 5
		updated, err := s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{MCPMinCalls: &calls, MCPForMinutes: &duration})
		if err != nil || updated.MCPMinCalls != 40 || updated.MCPForMinutes != 5 {
			t.Fatalf("update: %+v %v", updated, err)
		}
		loaded, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil || loaded.MCPMinCalls != 40 || loaded.MCPForMinutes != 5 {
			t.Fatal("settings did not persist")
		}
		for _, action := range []string{"rollback", "demote", "promote"} {
			if _, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{Action: &action}); err == nil {
				t.Fatal("deployment action accepted")
			}
		}
		invalid := 19
		if _, err = s.UpdateAlertRule(ctx, rule.ID, state.UpdateAlertRuleParams{MCPMinCalls: &invalid}); err == nil {
			t.Fatal("invalid traffic guard accepted")
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, state.NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, state.NewPgStore(pgtest.OpenMigrated(t))) })
}
