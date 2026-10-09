package state

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
)

func validateWorkflowPolicyEvidence(def OperationDefinition, report api.OperationWorkflowStateReport, payloads map[string][]byte) error {
	if report.FromState == "" && len(report.EvidenceMilestones) > 0 {
		if len(report.EvidenceMilestones) != 1 {
			return fmt.Errorf("%w: reconciliation requires exactly one discrepancy fact", ErrInvalidArgument)
		}
		evidence := report.EvidenceMilestones[0]
		reconciliation, err := api.ParseOperationWorkflowReconciliation(payloads[evidence.ID])
		if err != nil || reconciliation == nil || !reconciliation.RefreshNeeded() || reconciliation.Workflow != report.Workflow || reconciliation.InstanceID != report.InstanceID || reconciliation.AuthoritativeState != report.State || reconciliation.ContractVersion != report.ContractVersion {
			return fmt.Errorf("%w: snapshot requires matching reconciliation evidence", ErrInvalidArgument)
		}
		permitted := false
		for _, step := range def.Spec.WorkflowSteps {
			if step.Workflow == report.Workflow && step.AllowReconciliation && step.Milestone == evidence.Name {
				permitted = true
			}
		}
		if !permitted {
			return fmt.Errorf("%w: reconciliation is not enabled for this milestone", ErrInvalidArgument)
		}
	}
	if report.FromState == "" || report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
		return nil
	}
	for _, step := range def.Spec.WorkflowSteps {
		if step.Workflow != report.Workflow {
			continue
		}
		for _, edge := range step.Transitions {
			if edge.From != report.FromState || edge.To != report.State {
				continue
			}
			for _, requirement := range edge.RequiredEffects {
				matched := false
				for _, evidence := range report.EvidenceMilestones {
					if evidence.Name != requirement.Milestone {
						continue
					}
					effect, err := api.ParseOperationBusinessEffect(payloads[evidence.ID])
					if err == nil && effect != nil && api.OperationWorkflowEffectEvidenceReason(requirement, evidence.Name, report.Workflow, report.InstanceID, report.State, def.Spec.Name, *effect) == "" {
						matched = true
					}
				}
				if !matched {
					return fmt.Errorf("%w: confirmed effect evidence missing or mismatched for %s", ErrInvalidArgument, requirement.Milestone)
				}
			}
			for _, requirement := range edge.RequiredInvariants {
				matched := false
				for _, evidence := range report.EvidenceMilestones {
					if evidence.Name != requirement.Milestone {
						continue
					}
					invariant, err := api.ParseOperationBusinessInvariant(payloads[evidence.ID])
					if err == nil && invariant != nil && api.OperationWorkflowInvariantEvidenceReason(requirement, evidence.Name, report.Workflow, report.InstanceID, report.FromState, def.Spec.Name, *invariant) == "" {
						matched = true
					}
				}
				if !matched {
					return fmt.Errorf("%w: required passing invariant evidence missing or mismatched for %s", ErrInvalidArgument, requirement.Milestone)
				}
			}
			for _, policy := range edge.RequiredPolicies {
				matched := false
				for _, evidence := range report.EvidenceMilestones {
					if evidence.Name != policy.Milestone {
						continue
					}
					decision, err := api.ParseOperationBusinessDecision(payloads[evidence.ID])
					if err == nil && decision != nil && api.OperationWorkflowPolicyMatches(policy, evidence.Name, report.Workflow, report.InstanceID, *decision) {
						matched = true
					}
				}
				if !matched {
					return fmt.Errorf("%w: policy evidence missing or mismatched for %s", ErrInvalidArgument, policy.Milestone)
				}
			}
		}
	}
	return nil
}

func requiredWorkflowEvidenceIDs(def OperationDefinition, report api.OperationWorkflowStateReport) []string {
	names := map[string]bool{}
	if report.FromState == "" || report.BlockersOnly || report.DeadlineOnly || report.OutcomeOnly || report.DependenciesOnly {
		return nil
	}
	for _, step := range def.Spec.WorkflowSteps {
		if step.Workflow != report.Workflow {
			continue
		}
		for _, edge := range step.Transitions {
			if edge.From == report.FromState && edge.To == report.State {
				for _, effect := range edge.RequiredEffects {
					names[effect.Milestone] = true
				}
				for _, requirement := range edge.RequiredInvariants {
					names[requirement.Milestone] = true
				}
			}
		}
	}
	ids := []string{}
	for _, evidence := range report.EvidenceMilestones {
		if names[evidence.Name] {
			ids = append(ids, evidence.ID)
		}
	}
	return ids
}
