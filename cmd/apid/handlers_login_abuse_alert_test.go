package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPreAuthTargetAlertRejectsDeploymentActions(t *testing.T) {
	action := "rollback"
	req := api.CreateAlertRuleRequest{
		Metric: "pre_auth_target_threshold", Comparison: "gt", Threshold: 5,
		WindowSpec: "15m", WebhookURL: "https://example.com/hook", WebhookSecret: "secret",
		Action: &action,
	}
	if prob := validateAlertRuleBody(req); prob == nil || prob.Code != api.CodeAlertRuleInvalid {
		t.Fatalf("create validation = %#v, want invalid action", prob)
	}
	merged := state.AlertRule{
		Name: "login alert", Metric: state.AlertMetricPreAuthTargetThreshold,
		Comparison: state.AlertGt, Threshold: 5, WindowSpec: state.AlertWindow15m,
		WebhookURL: "https://example.com/hook", CooldownMinutes: 60,
		Action: state.AlertActionRollback,
	}
	if prob := validateAlertRuleRowUpdate(merged); prob == nil || prob.Code != api.CodeAlertRuleInvalid {
		t.Fatalf("update validation = %#v, want invalid action", prob)
	}
	req.Action = nil
	if prob := validateAlertRuleBody(req); prob != nil {
		t.Fatalf("webhook-only create rejected: %v", prob)
	}
	merged.Action = state.AlertActionWebhook
	if prob := validateAlertRuleRowUpdate(merged); prob != nil {
		t.Fatalf("webhook-only update rejected: %v", prob)
	}
}

func TestLoginTargetPresetRejectsDeploymentAction(t *testing.T) {
	store := state.NewMemStore()
	store.SeedAlertPresetForTest(state.AlertPreset{
		Name: "login_target_pressure", Metric: "pre_auth_target_threshold",
		MinimumPlan: "hobby", EnabledInCatalog: true,
	})
	s := &server{store: store}
	action := "rollback"
	_, prob := s.enableAlertPresetFromForm(context.Background(), state.Account{Plan: api.PlanHobby},
		"unused", "login_target_pressure", api.EnableAlertPresetRequest{
			WebhookURL: "https://example.com/hook", WebhookSecret: "secret", Action: &action,
		})
	if prob == nil || prob.Code != api.CodeAlertPresetInvalid {
		t.Fatalf("preset enable = %#v, want notification-only rejection", prob)
	}
}
