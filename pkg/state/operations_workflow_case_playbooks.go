package state

import (
	"context"
	"errors"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

func projectWorkflowCasePlaybooks(ctx context.Context, store workflowCaseBackend, account, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, out *api.OperationWorkflowCase) error {
	out.Guidance = []api.OperationWorkflowPlaybookGuidance{}
	out.GuidanceStatus = "evidence_unavailable"
	instance := out.Instance
	if instance == nil || instance.State == nil || instance.State.Stale || instance.ContractVersion != effectiveWorkflowContractVersion(instance.State.ContractVersion) {
		return nil
	}
	state := instance.State
	op, err := store.OperationByID(ctx, account, tenant, state.OperationID)
	if errors.Is(err, ErrNotFound) {
		out.GuidanceStatus = "definition_unavailable"
		return nil
	}
	if err != nil {
		return err
	}
	if !sameOperationHistoryIdentity(op.AppID, opts.AppID) || op.Scope != opts.Scope || op.Subject == nil || *op.Subject != out.Timeline.Subject || !sameOperationHistoryIdentity(op.PlatformTenantID, tenant) {
		out.GuidanceStatus = "definition_unavailable"
		return nil
	}
	def, err := store.OperationDefinitionByID(ctx, account, op.DefinitionID)
	if errors.Is(err, ErrNotFound) {
		out.GuidanceStatus = "definition_unavailable"
		return nil
	}
	if err != nil {
		return err
	}
	if !sameOperationHistoryIdentity(def.AppID, op.AppID) || def.Scope != op.Scope {
		out.GuidanceStatus = "definition_unavailable"
		return nil
	}
	rules := map[string]api.OperationWorkflowPlaybook{}
	for _, step := range def.Spec.WorkflowSteps {
		if step.Workflow != opts.Workflow || effectiveWorkflowContractVersion(step.Version) != instance.ContractVersion {
			continue
		}
		for _, r := range step.Playbooks {
			if prior, ok := rules[r.ID]; ok && readinessDigest(prior) != readinessDigest(r) {
				out.GuidanceStatus = "definition_unavailable"
				return nil
			}
			rules[r.ID] = r
		}
	}
	candidates := []api.OperationWorkflowPlaybook{}
	for _, r := range rules {
		candidates = append(candidates, r)
	}
	candidates, err = api.CanonicalOperationWorkflowPlaybooks(candidates)
	if err != nil {
		out.GuidanceStatus = "definition_unavailable"
		return nil
	}
	out.GuidanceStatus = "not_declared"
	if len(candidates) == 0 {
		return nil
	}
	out.GuidanceStatus = "available"
	if state.Terminal {
		return nil
	}
	if instance.Readiness != nil && instance.Readiness.HasMore {
		out.GuidanceTruncated = true
	}
	for _, r := range candidates {
		if !workflowCasePlaybookMatches(instance, r) {
			continue
		}
		if len(out.Guidance) >= api.OperationWorkflowPlaybooksMax {
			out.GuidanceTruncated = true
			break
		}
		out.Guidance = append(out.Guidance, api.OperationWorkflowPlaybookGuidance{Playbook: r, ContractVersion: instance.ContractVersion, ReportingOperationID: state.OperationID})
	}
	return nil
}
func workflowCasePlaybookMatches(instance *api.OperationWorkflowInstanceSnapshot, r api.OperationWorkflowPlaybook) bool {
	if r.MatchKind == "blocker" {
		for _, b := range instance.State.Blockers {
			if b.Code == r.MatchCode {
				return true
			}
		}
	}
	if r.MatchKind == "pending_verification" {
		for _, p := range instance.ReadinessVerifications {
			if p.Status == "awaiting_verification" && p.Resolution.Code == r.MatchCode {
				return true
			}
		}
	}
	if instance.Readiness != nil {
		for _, edge := range instance.Readiness.Items {
			switch r.MatchKind {
			case "missing_milestone":
				if slices.Contains(edge.MissingMilestones, r.MatchCode) {
					return true
				}
			case "missing_dependency":
				if slices.Contains(edge.MissingDependencyWorkflows, r.MatchCode) {
					return true
				}
				for _, d := range edge.UnmetDependencies {
					if d.Dependency.Workflow == r.MatchCode {
						return true
					}
				}
			case "requirement":
				if slices.Contains(edge.Reasons, r.MatchCode) {
					return true
				}
			}
		}
	}
	return false
}
