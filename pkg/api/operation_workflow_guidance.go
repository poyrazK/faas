package api

import (
	"fmt"
	"time"
)

// Guidance explains the retained readiness result without granting permission to
// change application state. Planned evidence must accompany the transition;
// missing evidence here does not prove that the business event never occurred.
func (r OperationWorkflowTransitionReadiness) Guidance() []string {
	var out []string
	for _, reason := range r.Reasons {
		switch reason {
		case "state_unknown":
			out = append(out, "Report the current workflow state before evaluating this transition.")
		case "transition_undeclared":
			out = append(out, "Declare this operation and state edge in a new workflow contract version.")
		case "path_selection_invalid":
			out = append(out, "Choose a path declared in the selected transition and pinned workflow contract.")
		case "terminal":
			out = append(out, "This workflow instance has completed; inspect its outcome before starting another instance.")
		case "from_state_mismatch", "revision_mismatch", "contract_version_mismatch":
			out = append(out, fmt.Sprintf("Refresh the workflow snapshot and evaluate against contract version %d and state revision %d.", r.ContractVersion, r.StateRevision))
		case "application_blocked", "dependency_unmet", "dependency_required", "milestone_required", "policy_evidence_required", "invariant_evidence_required", "effect_evidence_required", "retained_milestone_required", "retained_milestone_invalidated", "retained_milestone_expired", "retained_milestone_group_unmet":
			// The structured findings below explain these requirements individually.
		default:
			out = append(out, "Inspect readiness requirement: "+reason+".")
		}
	}
	for _, b := range r.Blockers {
		message := fmt.Sprintf("Resolve blocker %s: %s", b.Code, b.Description)
		if b.Owner != "" {
			message += " Owner: " + b.Owner + "."
		}
		if b.NextAction != "" {
			message += " Next action: " + b.NextAction
		}
		out = append(out, message)
	}
	for _, workflow := range r.MissingDependencyWorkflows {
		out = append(out, fmt.Sprintf("Report a prerequisite link to workflow %s.", workflow))
	}
	for _, related := range r.UnmetDependencies {
		d := related.Dependency
		message := fmt.Sprintf("Wait for prerequisite %s instance %s (%s:%s); reported status: %s.", d.Workflow, d.InstanceID, d.SubjectType, d.SubjectID, related.Status)
		if d.RequiredOutcomeCode != "" {
			message += " Required outcome: " + d.RequiredOutcomeCode + "."
		}
		out = append(out, message)
	}
	for _, result := range r.RetainedMilestones {
		p := result.Requirement
		if result.Status == "verified" && result.Evidence != nil {
			out = append(out, fmt.Sprintf("Verified retained milestone %s from operation %s (fact %s, operation instance %s).", p.Milestone, p.Operation, result.Evidence.ID, result.Evidence.OperationID))
		} else if result.Status == "expired" && result.Evidence != nil && result.Freshness != nil && result.Freshness.ExpiresAt != nil {
			out = append(out, fmt.Sprintf("Retained milestone %s from operation %s (fact %s) expired at %s. Renew this prerequisite before proceeding.", p.Milestone, p.Operation, result.Evidence.ID, result.Freshness.ExpiresAt.UTC().Format(time.RFC3339Nano)))
		} else if result.Status == "invalidated" && result.Evidence != nil && result.Invalidation != nil {
			i := result.Invalidation
			out = append(out, fmt.Sprintf("Retained milestone %s from operation %s (fact %s) was invalidated by %s from operation %s (fact %s, operation instance %s).", p.Milestone, p.Operation, result.Evidence.ID, i.Source.Milestone, i.Source.Operation, i.Evidence.ID, i.Evidence.OperationID))
		} else {
			out = append(out, fmt.Sprintf("Waiting for retained milestone %s from operation %s in this workflow instance and contract version. No matching fact is currently eligible.", p.Milestone, p.Operation))
		}
	}
	for _, result := range r.RetainedMilestones {
		if recovery := result.Recovery; recovery != nil {
			message := fmt.Sprintf("Recover through operation %s: check authoritative business rows and the operation requirements, perform any needed business action, commit and publish milestone %s in this workflow instance and contract version, then recheck this transition's readiness.", recovery.Operation, recovery.Milestone)
			if recovery.AfterOccurredAt != nil {
				message += " The new fact must occur strictly after " + recovery.AfterOccurredAt.UTC().Format(time.RFC3339Nano) + "."
			}
			if result.Freshness != nil {
				message += fmt.Sprintf(" The new fact must also be younger than %d seconds at the next readiness check.", result.Freshness.MaxAgeSeconds)
			}
			out = append(out, message)
		}
	}
	for _, group := range r.RetainedMilestoneGroups {
		if len(group.Paths) > 0 {
			if group.SelectedPathID != "" {
				out = append(out, fmt.Sprintf("The application selected path %s in group %s; another valid path does not satisfy this choice.", group.SelectedPathID, group.ID))
			}
			if group.Satisfied {
				for _, path := range group.Paths {
					if path.Satisfied && (group.SelectedPathID == "" || group.SelectedPathID == path.ID) {
						out = append(out, fmt.Sprintf("Alternative group %s is satisfied by complete path %s.", group.ID, path.ID))
						break
					}
				}
			} else {
				out = append(out, fmt.Sprintf("Satisfy one complete path in retained prerequisite group %s; every requirement within a chosen path must pass.", group.ID))
				for _, path := range group.Paths {
					if group.SelectedPathID != "" && group.SelectedPathID != path.ID {
						continue
					}
					out = append(out, fmt.Sprintf("Path %s in group %s:", path.ID, group.ID))
					nested := OperationWorkflowTransitionReadiness{RetainedMilestones: path.Requirements}
					out = append(out, nested.Guidance()...)
				}
			}
			continue
		}
		if group.Satisfied {
			for _, alternative := range group.Alternatives {
				if alternative.Status == "verified" {
					out = append(out, fmt.Sprintf("Alternative group %s is satisfied by milestone %s from operation %s.", group.ID, alternative.Requirement.Milestone, alternative.Requirement.Operation))
					break
				}
			}
			continue
		}
		out = append(out, fmt.Sprintf("Satisfy any one alternative in retained prerequisite group %s; the alternatives below are choices, not cumulative requirements.", group.ID))
		nested := OperationWorkflowTransitionReadiness{RetainedMilestones: group.Alternatives}
		out = append(out, nested.Guidance()...)
	}
	for _, milestone := range r.MissingMilestones {
		out = append(out, fmt.Sprintf("Include milestone %s with the proposed transition.", milestone))
	}
	for _, p := range r.MissingPolicies {
		out = append(out, fmt.Sprintf("Include milestone %s with decision %s from rule %s version %s.", p.Milestone, p.Code, p.RuleID, p.RuleVersion))
	}
	for _, i := range r.UnmetInvariants {
		p := i.Requirement
		out = append(out, fmt.Sprintf("Include passing invariant %s version %s in milestone %s; evidence status: %s.", p.Code, p.Version, p.Milestone, i.Reason))
	}
	for _, e := range r.UnmetEffects {
		p := e.Requirement
		out = append(out, fmt.Sprintf("Include confirmed effect %s version %s in milestone %s; evidence status: %s.", p.Code, p.Version, p.Milestone, e.Reason))
	}
	if r.Ready {
		out = append(out, "Declared requirements are met. The application must check its business row and authorization before committing the transition.")
	}
	return out
}
