package api

import "fmt"

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
		case "terminal":
			out = append(out, "This workflow instance has completed; inspect its outcome before starting another instance.")
		case "from_state_mismatch", "revision_mismatch", "contract_version_mismatch":
			out = append(out, fmt.Sprintf("Refresh the workflow snapshot and evaluate against contract version %d and state revision %d.", r.ContractVersion, r.StateRevision))
		case "application_blocked", "dependency_unmet", "dependency_required", "milestone_required", "policy_evidence_required", "invariant_evidence_required", "effect_evidence_required":
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
