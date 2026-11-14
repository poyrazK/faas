package mcpmanagement

import "github.com/onebox-faas/faas/pkg/api"

// Deliberately construct every public field. Stored plans/native DTOs can carry
// credentials, request keys, raw configuration or future sensitive fields.
func (m *management) projectSavedAppPlan(p *releasePlan) (map[string]any, error) {
	out := map[string]any{"plan_id": p.ID, "action": p.Action, "account_id": p.AccountID, "api_base": p.APIBase, "app": p.App, "app_id": p.AppID, "scope": p.Scope, "expires_at": p.ExpiresAt, "reason": m.clean(p.Reason), "traffic_effect": m.clean(p.TrafficEffect)}
	if p.EnvironmentID != "" {
		out["project"], out["project_id"], out["environment_id"] = p.Project, p.ProjectID, p.EnvironmentID
	}
	switch p.Action {
	case "deploy", "rollback", "workload_rollout", "service_abort":
		out["baseline"] = map[string]any{"configuration_hash": p.ConfigurationHash, "workload_revision": p.WorkloadRevision, "workload_config_hash": p.WorkloadConfigHash, "current_deployment_id": p.ExpectedCurrentID, "latest_deployment_id": p.latestID}
		if p.Image != "" {
			out["image"] = p.Image
		}
		if s := p.Source; s != nil {
			out["source"] = map[string]any{"repo": s.Repo, "commit_sha": s.CommitSHA, "requested_ref": m.clean(p.RequestedRef), "installation_id": s.InstallationID, "repository_id": s.RepositoryID, "source_root": m.clean(s.SourceRoot)}
		}
		if p.TargetDeploymentID != "" {
			out["target_deployment_id"] = p.TargetDeploymentID
		}
		if p.PredecessorDeploymentID != "" {
			out["predecessor_deployment_id"] = p.PredecessorDeploymentID
		}
		out["rollout_mode"] = p.RolloutMode
		if policy := p.RolloutPolicy; policy != nil {
			out["rollout_policy"] = m.savedRolloutPolicy(policy)
		}
	case "route_budgets":
		if p.Budgets == nil {
			return nil, invalid("invalid_plan", "saved route budget review is missing")
		}
		b, req := p.Budgets.Before, p.Budgets.Proposed
		out["route_budgets"] = map[string]any{
			"before":   map[string]any{"revision": b.Revision, "app_revision": b.AppRevision, "budget_source": b.BudgetSource, "enabled": b.Config.Enabled, "routes": b.Config.Routes},
			"proposed": map[string]any{"expected_revision": req.ExpectedRevision, "expected_app_revision": req.ExpectedAppRevision, "inherit": req.Inherit, "enabled": req.Enabled, "routes": req.Routes},
		}
	case "regression_action":
		if err := validateRegressionPlan(p); err != nil {
			return nil, err
		}
		b, req := p.Regression.Before, p.Regression.Proposed
		out["regression_triage"] = map[string]any{"deployment_id": b.DeploymentID, "route": b.Route, "before_state": b.State, "expected_version": req.ExpectedVersion, "action": req.Action, "dismissed_until": req.DismissedUntil}
	default:
		return nil, invalid("invalid_plan", "saved plan action is unsupported")
	}
	return out, nil
}

func (m *management) savedRolloutPolicy(p *rolloutPolicy) map[string]any {
	out := map[string]any{"memory_mb_per_replica": p.MemoryMBPerReplica, "cpu_millicores": p.CPUMillicores, "max_concurrency": p.MaxConcurrency, "health_path": m.clean(p.HealthPath), "stop_signal": m.clean(p.StopSignal), "stop_grace_period_seconds": p.StopGracePeriodSeconds}
	if r := p.ServiceReplicas; r != nil {
		out["service_replicas"] = map[string]any{"min": r.Min, "max": r.Max, "desired": r.Desired}
	}
	if r := p.WorkerReplicas; r != nil {
		out["worker_replicas"] = map[string]any{"min": r.Min, "max": r.Max, "metric": m.clean(r.Metric), "name": m.clean(r.Name), "target": r.Target, "observer_app_slug": r.ObserverAppSlug}
	}
	return out
}

func (m *management) projectSavedEnvironmentPlan(p *environmentPlan) map[string]any {
	changes := make([]map[string]any, 0, len(p.Changes))
	for _, c := range p.Changes {
		changes = append(changes, m.savedEnvironmentChange(c))
	}
	out := map[string]any{"plan_id": p.ID, "action": p.Action, "account_id": p.AccountID, "api_base": p.APIBase, "project": p.Project, "project_id": p.ProjectID, "from_environment": p.FromEnvironment, "from_environment_id": p.FromID, "environment": p.Environment, "environment_id": p.EnvironmentID, "expires_at": p.ExpiresAt, "reason": m.clean(p.Reason), "traffic_effect": m.clean(p.TrafficEffect), "approval_required": p.ApprovalRequired, "change_coverage": "saved_workload_release_metadata", "changes": changes}
	if p.Action == "environment_rollback" {
		delete(out, "changes")
		out["change_coverage"] = "promotion_reference_only"
	}
	if p.PromotionID != "" {
		out["promotion_id"] = p.PromotionID
	}
	return out
}

func (m *management) savedEnvironmentChange(c api.ProjectEnvironmentPromotionChange) map[string]any {
	return map[string]any{"workload_slug": c.WorkloadSlug, "workload_name": m.clean(c.WorkloadName), "kind": c.Kind, "source_deployment_id": c.SourceDeploymentID, "target_deployment_id": c.TargetDeploymentID, "source_build_id": c.SourceBuildID, "target_build_id": c.TargetBuildID, "source_revision": m.clean(c.SourceRevision), "target_revision": m.clean(c.TargetRevision), "source_revision_kind": c.SourceRevisionKind, "target_revision_kind": c.TargetRevisionKind, "release_configuration_hash": c.ReleaseConfigurationHash, "source_workload_config_hash": c.SourceWorkloadConfigHash, "target_workload_config_hash": c.TargetWorkloadConfigHash, "promoted_workload_config_hash": c.PromotedWorkloadConfigHash}
}
