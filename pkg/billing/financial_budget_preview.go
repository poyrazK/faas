package billing

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// PreviewFinancialBudget performs no writes and never claims enforcement from
// a saved policy. Financial observations and current live members are distinct.
func PreviewFinancialBudget(ctx context.Context, store state.Store, account string, spec financial.BudgetSpec, now time.Time) (api.FinancialBudgetPreviewResponse, error) {
	if now.IsZero() {
		return api.FinancialBudgetPreviewResponse{}, state.ErrInvalidArgument
	}
	spec.NotifyMillicents = append([]int64{}, spec.NotifyMillicents...)
	budgets, ok := store.(state.FinancialBudgetStore)
	if !ok {
		return api.FinancialBudgetPreviewResponse{}, ErrFinancialUnavailable
	}
	if err := budgets.ValidateFinancialBudgetScope(ctx, account, spec); err != nil {
		return api.FinancialBudgetPreviewResponse{}, err
	}
	start := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	report, err := ReadFinancialCosts(ctx, store, account, start, now)
	if err != nil {
		return api.FinancialBudgetPreviewResponse{}, err
	}
	out := api.FinancialBudgetPreviewResponse{Spec: spec, PeriodStart: start, PeriodEnd: report.PeriodEnd, AsOf: report.AsOf, CoverageComplete: true, Fresh: true, Reasons: []string{}, Targets: []api.FinancialBudgetTarget{}, ContinuingTargets: []api.FinancialBudgetTarget{}}
	observation, err := EvaluateFinancialBudget(report, spec, now)
	if err != nil {
		return out, err
	}
	out.KnownMillicents = *observation.KnownMillicents
	out.CoverageComplete, out.Fresh, out.Reasons = observation.CoverageComplete, observation.Fresh, observation.Reasons
	out.KnownLimitReached = out.KnownMillicents >= spec.LimitMillicents
	// Promotion is pending the durable decisions, owner integrations and
	// native lifecycle acceptance in ADR-566. A preview never activates intent.
	out.EnforcementReady = false
	_, stopping := store.(state.FinancialBudgetStopStore)
	_, commands := store.(state.FinancialPreviewCommandStore)
	if stopping && commands && financial.PreviewStopping(spec) {
		out.EnforcementReady = true
	}
	if financial.NotificationOnly(spec) {
		out.Reasons = append(out.Reasons, "notification_only_workloads_continue")
	} else if financial.PreviewStopping(spec) && out.EnforcementReady {
		out.Reasons = append(out.Reasons, "monitored_preview_stopping", "production_can_continue", "overshoot_possible")
	} else {
		out.Reasons = append(out.Reasons, "enforcement_integration_pending")
	}
	out.Guarantee = "monitored_after_retained_evidence; delayed_sources_and_drain_can_exceed_limit"
	if financial.NotificationOnly(spec) {
		out.Guarantee = "notification_only_after_retained_evidence;workloads_continue"
	}
	if spec.Mode == "strict" {
		out.Guarantee = "strict_compute_pending_reservations_and_local_deadline_acceptance"
		if financial.StrictCompute(spec) {
			if compute, ok := store.(state.FinancialComputeStore); ok {
				out.EnforcementReady, err = compute.FinancialComputeReady(ctx, account, financial.ComputeScopeKey(spec.Scope))
				if err != nil {
					return out, err
				}
				out.Guarantee = "strict_app_gross_compute_funded_windows;conservative_commitments;other_meters_excluded"
				if financial.StrictJobCompute(spec) {
					out.Guarantee = "strict_job_gross_compute_funded_windows;conservative_commitments;other_meters_excluded"
				}
				if financial.StrictProjectCompute(spec) {
					out.Guarantee = "strict_project_gross_compute_funded_windows;shared_project_book;project_apps_only;conservative_commitments;other_meters_excluded"
				}
				if financial.StrictAccountEgress(spec) {
					ready, ok := store.(state.FinancialEgressReadinessStore)
					out.EnforcementReady = false
					if ok {
						out.EnforcementReady, err = ready.FinancialEgressReady(ctx, account)
						if err != nil {
							return out, err
						}
					}
					out.Guarantee = "strict_shared_account_compute_egress;separate_quantity_allowances;kernel_byte_credits;unused_credits_commit_conservatively;other_meters_excluded"
				} else if financial.StrictAccountCompute(spec) {
					out.Guarantee = "strict_account_net_compute_funded_windows;shared_quantity_allowance;funded_job_attempts;other_meters_excluded"
				}
				if !out.EnforcementReady {
					out.Reasons = append(out.Reasons, "requires_quiet_app_complete_accounting_native_owners")
				}
			}
		}
	}
	members, err := financialBudgetMembers(ctx, store, account, spec.Scope)
	if err != nil {
		return out, err
	}
	for _, member := range members {
		target := member.target
		selected := spec.Enabled && (spec.Action == "suspend_workloads" || spec.Action == "notify" || (spec.Action == "reject_traffic" && member.traffic) || (spec.Action == "stop_previews" && member.preview) || (spec.Action == "suspend_background" && member.background))
		if selected {
			target.Effect = financialBudgetEffect(spec.Action, member.background)
			out.Targets = append(out.Targets, target)
		}
		if !selected || spec.Action == "notify" || spec.Action == "reject_traffic" {
			target.Effect = "compute_can_continue"
			out.ContinuingTargets = append(out.ContinuingTargets, target)
		}
	}
	if !spec.Enabled {
		out.Reasons = append(out.Reasons, "policy_disabled")
	} else if len(out.Targets) == 0 {
		out.Reasons = append(out.Reasons, "no_current_action_targets")
	}
	return out, nil
}

func financialBudgetAttributionComplete(scope financial.BudgetScope, costs financial.ContractCosts) bool {
	for _, contract := range costs.Contracts {
		for _, allocation := range contract.Allocations {
			if allocation.Quantity > 0 && !scope.AttributionKnown(allocation.Attribution) {
				return false
			}
		}
	}
	return true
}

type financialBudgetMember struct {
	target                       api.FinancialBudgetTarget
	traffic, preview, background bool
}

func financialBudgetMembers(ctx context.Context, store state.Store, account string, scope financial.BudgetScope) ([]financialBudgetMember, error) {
	var environment state.ProjectEnvironment
	if scope.Kind == "environment" {
		lookup, ok := store.(interface {
			ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
		})
		if !ok {
			return nil, ErrFinancialUnavailable
		}
		var err error
		environment, err = lookup.ProjectEnvironmentByID(ctx, scope.ID)
		if err != nil {
			return nil, err
		}
		if environment.AccountID != account {
			return nil, state.ErrNotFound
		}
	}
	apps, err := store.ListApps(ctx, account)
	if err != nil {
		return nil, err
	}
	out := []financialBudgetMember{}
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.AccountID != account {
			continue
		}
		identity := financial.Attribution{AppID: app.ID, ProjectID: app.ProjectID}
		member := financialBudgetMember{target: api.FinancialBudgetTarget{Kind: "app", ID: app.ID, Name: app.Slug}, preview: app.PreviewOfSlug != "", background: app.WorkloadClass == state.WorkloadClassWorker || app.WorkloadClass == state.WorkloadClassJob}
		member.traffic = !member.background
		if scope.Kind == "environment" {
			if app.ProjectID != environment.ProjectID {
				continue
			}
			deployments, err := store.LiveDeployments(ctx, app.ID)
			if err != nil {
				return nil, err
			}
			for _, deployment := range deployments {
				matches, err := state.MatchFinancialBudgetEnvironment(ctx, store, app, environment.ID, deployment.ID)
				if err != nil {
					return nil, fmt.Errorf("%w: runtime scope ownership unavailable", ErrFinancialUnavailable)
				}
				if matches {
					member.target.EnvironmentID, member.target.DeploymentID = environment.ID, deployment.ID
					out = append(out, member)
				}
			}
		} else if scope.Matches(identity) {
			out = append(out, member)
		}
		if len(out) > api.FinancialAllocationMax {
			return nil, state.ErrFinancialAllocationLimit
		}
	}
	switch scope.Kind {
	case "job":
		job, err := store.JobGetByID(ctx, scope.ID)
		if err != nil {
			return nil, err
		}
		if job.AccountID != account {
			return nil, state.ErrNotFound
		}
		out = append(out, financialBudgetMember{target: api.FinancialBudgetTarget{Kind: "job", ID: job.ID, Name: job.Name}, background: true})
	case "account":
		jobs, err := store.JobListByAccount(ctx, account, api.FinancialAllocationMax+1, 0)
		if err != nil {
			return nil, err
		}
		for _, job := range jobs {
			if job.Status != "deleted" && job.AccountID == account && scope.Matches(financial.Attribution{JobID: job.ID}) {
				out = append(out, financialBudgetMember{target: api.FinancialBudgetTarget{Kind: "job", ID: job.ID, Name: job.Name}, background: true})
			}
		}
	}
	if len(out) > api.FinancialAllocationMax {
		return nil, state.ErrFinancialAllocationLimit
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].target, out[j].target
		return fmt.Sprint(a.Kind, a.ID, a.EnvironmentID, a.DeploymentID) < fmt.Sprint(b.Kind, b.ID, b.EnvironmentID, b.DeploymentID)
	})
	return out, nil
}

func financialBudgetEffect(action string, background bool) string {
	switch action {
	case "notify":
		return "notify_only"
	case "reject_traffic":
		return "reject_new_traffic"
	default:
		if background {
			return "block_dispatch_then_terminate_at_drain_deadline"
		}
		if action == "stop_previews" {
			return "block_traffic_and_wakes_then_stop_at_drain_deadline"
		}
		return "block_traffic_and_wakes_then_park_at_drain_deadline"
	}
}
