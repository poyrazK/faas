package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPreAuthTargetAlertRejectsDeploymentActions(t *testing.T) {
	for _, metric := range []state.AlertMetric{state.AlertMetricPreAuthTargetThreshold, state.AlertMetricPreAuthTargetSignalGapPct} {
		action := "rollback"
		req := api.CreateAlertRuleRequest{
			Metric: string(metric), Comparison: "gt", Threshold: 5,
			WindowSpec: "15m", WebhookURL: "https://example.com/hook", WebhookSecret: "secret",
			Action: &action,
		}
		if prob := validateAlertRuleBody(req); prob == nil || prob.Code != api.CodeAlertRuleInvalid {
			t.Fatalf("%s create validation = %#v, want invalid action", metric, prob)
		}
		merged := state.AlertRule{
			Name: "login alert", Metric: metric,
			Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow15m,
			WebhookURL: "https://example.com/hook", CooldownMinutes: 60,
			Action: state.AlertActionRollback,
		}
		if prob := validateAlertRuleRowUpdate(merged); prob == nil || prob.Code != api.CodeAlertRuleInvalid {
			t.Fatalf("%s update validation = %#v, want invalid action", metric, prob)
		}
		req.Action = nil
		if prob := validateAlertRuleBody(req); prob != nil {
			t.Fatalf("%s webhook-only create rejected: %v", metric, prob)
		}
		merged.Action = state.AlertActionWebhook
		if prob := validateAlertRuleRowUpdate(merged); prob != nil {
			t.Fatalf("%s webhook-only update rejected: %v", metric, prob)
		}
	}
}

func TestLoginTargetPresetRejectsDeploymentAction(t *testing.T) {
	store := state.NewMemStore()
	for _, preset := range []state.AlertPreset{
		{Name: "login_target_pressure", Metric: "pre_auth_target_threshold", MinimumPlan: "hobby", EnabledInCatalog: true},
		{Name: "login_target_signal_health", Metric: "pre_auth_target_signal_gap_pct", MinimumPlan: "hobby", EnabledInCatalog: true},
	} {
		store.SeedAlertPresetForTest(preset)
	}
	s := &server{store: store}
	action := "rollback"
	for _, name := range []string{"login_target_pressure", "login_target_signal_health"} {
		_, prob := s.enableAlertPresetFromForm(context.Background(), state.Account{Plan: api.PlanHobby},
			"unused", name, api.EnableAlertPresetRequest{
				WebhookURL: "https://example.com/hook", WebhookSecret: "secret", Action: &action,
			})
		if prob == nil || prob.Code != api.CodeAlertPresetInvalid {
			t.Fatalf("%s preset enable = %#v, want notification-only rejection", name, prob)
		}
	}
}
