// adr: 904
package alerts

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkflowInvestigationLinksRespectOwnershipAndMetric(t *testing.T) {
	store := state.NewMemStore()
	account, err := store.CreateAccount(t.Context(), "notification-links@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "billing", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	evaluator := NewEvaluator(EvaluatorOptions{Store: store})
	for _, tc := range []struct {
		name, account, app string
		metric             state.AlertMetric
		linked             bool
	}{
		{"owned failure", account.ID, app.ID, state.AlertMetricWorkflowFailures, true},
		{"owned backlog", account.ID, app.ID, state.AlertMetricWorkflowDueAge, true},
		{"account-wide", account.ID, "", state.AlertMetricWorkflowFailures, false},
		{"foreign account", "other-account", app.ID, state.AlertMetricWorkflowFailures, false},
		{"missing app", account.ID, "missing-app", state.AlertMetricWorkflowFailures, false},
		{"unrelated metric", account.ID, app.ID, state.AlertMetricLatencyP95, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := state.AlertRule{AccountID: tc.account, AppID: tc.app, Metric: tc.metric}
			_, payload, err := buildPayload(rule, 1, evaluator.investigationPaths(context.Background(), rule))
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := payload["workflow_runs_path"]; ok != tc.linked {
				t.Fatalf("unexpected links: %+v", payload)
			}
			if !tc.linked {
				if _, ok := payload["app_id"]; ok {
					t.Fatal("unresolved ownership disclosed app context")
				}
				return
			}
			if payload["app_id"] != app.ID || payload["automations_path"] != "/v1/apps/billing/automations" {
				t.Fatalf("wrong app context: %+v", payload)
			}
			if tc.metric == state.AlertMetricWorkflowFailures {
				if payload["workflow_runs_path"] != "/v1/apps/billing/workflows/runs?status=failed" || payload["workflow_dead_runs_path"] != "/v1/apps/billing/workflows/runs?status=dead" {
					t.Fatalf("missing terminal statuses: %+v", payload)
				}
			} else if payload["workflow_runs_path"] != "/v1/apps/billing/workflows/runs" {
				t.Fatalf("unexpected filter: %+v", payload)
			}
		})
	}
}
