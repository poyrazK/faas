// Package financialbudgets projects retained monitoring state without implying
// that a notification policy enforces a spending ceiling.
package financialbudgets

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func Response(ctx context.Context, store state.Store, p state.FinancialBudget, now time.Time) (api.FinancialBudgetResponse, error) {
	out := api.FinancialBudgetResponse{ID: p.ID, AccountID: p.AccountID, Revision: p.Revision, Spec: p.Spec, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, DeletedAt: p.DeletedAt, Status: "draft", Reasons: []string{"enforcement_integration_pending"}}
	if p.DeletedAt != nil {
		out.Status = "deleted"
		return out, nil
	}
	if !p.Spec.Enabled {
		return out, nil
	}
	out.Status = "unavailable"
	if financial.StrictCompute(p.Spec) {
		compute, ok := store.(state.FinancialComputeStore)
		if !ok {
			return out, nil
		}
		balance, err := compute.FinancialComputeBalance(ctx, p, now)
		if err != nil {
			return out, err
		}
		out.Status = "active"
		out.EnforcementReady = true
		out.Runtime = &financial.BudgetRuntime{Compute: &balance, Alerts: []financial.BudgetAlert{}}
		out.Reasons = []string{"strict_app_gross_compute", "conservative_compute_commitments", "funded_execution_window_only", "subscription_storage_egress_tax_excluded"}
		if financial.StrictAccountCompute(p.Spec) {
			out.Reasons[0] = "strict_account_net_compute"
			out.Reasons = append(out.Reasons, "shared_quantity_allowance", "funded_job_attempts")
		}
		if financial.StrictAccountEgress(p.Spec) {
			out.Reasons = []string{"strict_account_net_compute_egress", "one_paid_limit_two_quantity_allowances", "kernel_byte_credits", "unused_egress_credits_commit_conservatively", "egress_period_rate_pinned", "subscription_storage_builds_object_delivery_tax_excluded"}
		}
		if financial.StrictJobCompute(p.Spec) {
			out.Reasons[0] = "strict_job_gross_compute"
		}
		if financial.StrictProjectCompute(p.Spec) {
			out.Reasons[0] = "strict_project_gross_compute"
			out.Reasons = append(out.Reasons, "project_apps_only_jobs_excluded")
		}
		if !balance.Initialized {
			out.Reasons = append(out.Reasons, "awaiting_period_initialization")
		}
		if balance.Held {
			out.Reasons = append(out.Reasons, "compute_budget_held")
		}
		report, readErr := billing.ReadFinancialCosts(ctx, store, p.AccountID, financial.ComputeMonth(now), now)
		if readErr == nil {
			evaluation, evalErr := billing.EvaluateFinancialBudget(report, p.Spec, now)
			if evalErr != nil {
				return out, evalErr
			}
			out.Runtime.Evaluation = &evaluation
		} else {
			out.Reasons = append(out.Reasons, "cost_evidence_unavailable")
		}
		return out, nil
	}
	monitor, ok := store.(state.FinancialBudgetMonitorStore)
	if !ok || !financial.Monitored(p.Spec) {
		return out, nil
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	runtime, err := monitor.FinancialBudgetRuntime(ctx, p.AccountID, p.ID, p.Revision, start)
	if err != nil {
		return out, err
	}
	out.Status, out.NotificationReady, out.Runtime = "active", true, &runtime
	out.Reasons = []string{"notification_only_workloads_continue"}
	if financial.PreviewStopping(p.Spec) {
		stopping, ok := store.(state.FinancialBudgetStopStore)
		_, commands := store.(state.FinancialPreviewCommandStore)
		if !ok || !commands {
			out.Status, out.NotificationReady = "unavailable", false
			return out, nil
		}
		stops, err := stopping.ActiveFinancialBudgetStops(ctx, p.AccountID, now)
		if err != nil {
			return out, err
		}
		for _, stop := range stops {
			if stop.PolicyID == p.ID && stop.Revision == p.Revision {
				stop.Targets, err = stopping.FinancialBudgetStopTargets(ctx, p.AccountID, stop.ID)
				if err != nil {
					return out, err
				}
				for i := range stop.Targets {
					if now.Before(stop.Targets[i].ObservedAt) || now.Sub(stop.Targets[i].ObservedAt) > api.FinancialEvidenceFreshness {
						stop.Targets[i].State = "unavailable"
						stop.Targets[i].LastError = "scheduler_observation_stale"
					}
				}
				runtime.Stops = append(runtime.Stops, stop.BudgetStop)
			}
		}
		out.EnforcementReady = true
		out.Reasons = []string{"monitored_preview_stopping", "production_can_continue", "overshoot_possible"}
	}
	if runtime.Evaluation == nil {
		out.Reasons = append(out.Reasons, "awaiting_first_evaluation")
	} else if now.Before(runtime.Evaluation.EvaluatedAt) || now.Sub(runtime.Evaluation.EvaluatedAt) > api.FinancialEvidenceFreshness {
		runtime.Evaluation.Fresh = false
		runtime.Evaluation.Reasons = append(runtime.Evaluation.Reasons, "monitor_evaluation_stale")
	}
	return out, nil
}
