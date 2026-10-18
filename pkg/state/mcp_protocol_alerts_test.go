package state

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPProtocolAlertStoreRoundTrip(t *testing.T) {
	check := func(t *testing.T, s Store) {
		t.Helper()
		ctx := context.Background()
		acct, err := s.CreateAccount(ctx, "customer-alert@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		app, err := s.CreateApp(ctx, App{AccountID: acct.ID, Slug: "customer-alert-app", Type: AppTypeApp, Runtime: "node22", Status: AppActive})
		if err != nil {
			t.Fatal(err)
		}
		rule, err := s.CreateAlertRule(ctx, AlertRule{AccountID: acct.ID, AppID: app.ID, Name: "RPC failures", Metric: AlertMetric(api.MetricMCPProtocolErrorRate), Comparison: AlertGt, Threshold: 10, WindowSpec: "5m", Action: AlertActionWebhook, WebhookURL: "https://example.com/hook", WebhookSecretSealed: []byte("sealed-fixture"), CooldownMinutes: 15, Enabled: true})
		if err != nil || rule.MCPMinCalls != 20 || rule.MCPForMinutes != 10 {
			t.Fatalf("create: %+v %v", rule, err)
		}
		calls, duration := 40, 5
		updated, err := s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{MCPMinCalls: &calls, MCPForMinutes: &duration})
		if err != nil || updated.MCPMinCalls != 40 || updated.MCPForMinutes != 5 {
			t.Fatalf("update: %+v %v", updated, err)
		}
		loaded, err := s.AlertRuleByID(ctx, rule.ID)
		if err != nil || loaded.MCPMinCalls != 40 || loaded.MCPForMinutes != 5 {
			t.Fatal("settings did not persist")
		}
		for _, action := range []string{"rollback", "demote", "promote"} {
			if _, err = s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{Action: &action}); err == nil {
				t.Fatal("deployment action accepted")
			}
		}
		invalid := 19
		if _, err = s.UpdateAlertRule(ctx, rule.ID, UpdateAlertRuleParams{MCPMinCalls: &invalid}); err == nil {
			t.Fatal("invalid traffic guard accepted")
		}
	}
	t.Run("memory", func(t *testing.T) { check(t, NewMemStore()) })
	t.Run("postgres", func(t *testing.T) { check(t, NewPgStore(pgtest.OpenMigrated(t))) })
}
