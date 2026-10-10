package state

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/operations"
)

func validateWorkflowPolicyEvidence(def OperationDefinition, report api.OperationWorkflowStateReport, payloads map[string][]byte) error {
	if report.FromState != "" && !report.BlockersOnly && !report.DeadlineOnly && !report.OutcomeOnly && !report.DependenciesOnly {
		for _, step := range def.Spec.WorkflowSteps {
			if step.Workflow != report.Workflow {
				continue
			}
			for _, edge := range step.Transitions {
				if edge.From != report.FromState || edge.To != report.State {
					continue
				}
				for _, group := range edge.RetainedMilestoneGroups {
					requiresChoice := false
					for _, path := range group.Paths {
						requiresChoice = requiresChoice || path.Decision != nil
					}
					chosen := false
					for _, selection := range report.SelectedPaths {
						if selection.GroupID != group.ID {
							continue
						}
						chosen = true
						for _, path := range group.Paths {
							if path.ID != selection.PathID || path.Decision == nil {
								continue
							}
							matched := false
							for _, evidence := range report.EvidenceMilestones {
								if evidence.ID != selection.DecisionMilestoneID || evidence.Name != path.Decision.Milestone {
									continue
								}
								d, err := api.ParseOperationBusinessDecision(payloads[evidence.ID])
								matched = err == nil && d != nil && api.OperationWorkflowPolicyMatches(*path.Decision, evidence.Name, report.Workflow, report.InstanceID, *d)
							}
							if !matched {
								return fmt.Errorf("%w: path %s requires decision %s from rule %s version %s", ErrInvalidArgument, path.ID, path.Decision.Code, path.Decision.RuleID, path.Decision.RuleVersion)
							}
						}
					}
					if requiresChoice && !chosen {
						return fmt.Errorf("%w: decision-constrained group %s requires a selected path", ErrInvalidArgument, group.ID)
					}
				}
			}
		}
	}

	for _, selection := range report.SelectedPaths {
		if selection.DecisionMilestoneID == "" {
			continue
		}
		referenced := false
		for _, evidence := range report.EvidenceMilestones {
			if evidence.ID == selection.DecisionMilestoneID {
				referenced = true
			}
		}
		versionMatched := false
		for _, evidence := range report.EvidenceMilestones {
			if evidence.ID != selection.DecisionMilestoneID {
				continue
			}
			steps, err := operations.WorkflowStepsForMilestone(def.Spec, evidence.Name, payloads[evidence.ID])
			if err != nil {
				continue
			}
			for _, step := range steps {
				version := step.Version
				if version == 0 {
					version = 1
				}
				if step.Workflow == report.Workflow && step.InstanceID == report.InstanceID && version == report.ContractVersion {
					versionMatched = true
				}
			}
		}
		decision, err := api.ParseOperationBusinessDecision(payloads[selection.DecisionMilestoneID])
		if !referenced || !versionMatched || err != nil || decision == nil || decision.Workflow != report.Workflow || decision.InstanceID != report.InstanceID || decision.Code != selection.DecisionCode || decision.Description != selection.DecisionDescription || decision.RuleID != selection.DecisionRuleID || decision.RuleVersion != selection.DecisionRuleVersion {
			return fmt.Errorf("%w: selected path requires matching business decision evidence", ErrInvalidArgument)
		}
	}

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
