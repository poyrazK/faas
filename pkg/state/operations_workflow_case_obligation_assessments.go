package state

import "github.com/onebox-faas/faas/pkg/api"

// Declared transition requirements are context, not inferred guards on human coordination.
func projectWorkflowCaseObligationAssessments(out *api.OperationWorkflowCase, rows []api.OperationWorkflowReadinessFollowUp, tenant string, operator bool) {
	context := api.OperationWorkflowObligationContext{Status: "evidence_unavailable", Summary: "Current workflow requirements are unknown; obtain fresh application evidence.", CheckedAt: out.EvidenceCheckedAt, CoverageIncomplete: true}
	fresh := out.Instance != nil && out.Instance.State != nil && !out.Instance.State.Stale
	if fresh {
		state := out.Instance.State
		if state.Terminal {
			context.Status = "terminal_reported"
			context.Summary = "The application reports a terminal workflow; outstanding coordination still needs review and business success is not inferred."
			context.CoverageIncomplete = false
		} else if readiness := out.Instance.Readiness; readiness != nil {
			context.TransitionCount = readiness.TransitionCount
			context.EvaluatedTransitionCount = len(readiness.Items)
			context.CoverageIncomplete = readiness.HasMore
			ready := false
			for _, edge := range readiness.Items {
				ready = ready || edge.Declared && edge.Ready
				if !edge.Declared || edge.PendingVerificationsTruncated || edge.Reporting != nil && edge.Reporting.ConditionsTruncated {
					context.CoverageIncomplete = true
				}
			}
			switch {
			case ready:
				context.Status = "transition_requirements_satisfied"
				context.Summary = "At least one evaluated declared transition satisfies its requirements. The application still owns authorization and execution."
			case context.CoverageIncomplete:
				context.Status = "partial_readiness"
				context.Summary = "Evaluated transition context is incomplete; omitted requirements or transitions cannot be assessed."
			case len(readiness.Items) == 0 && readiness.TransitionCount == 0:
				context.Status = "no_declared_transition"
				context.Summary = "No current-state transition is declared in this projection; coordination obligations remain independent."
			case len(readiness.Items) > 0:
				context.Status = "waiting_on_requirements"
				context.Summary = "Evaluated declared transitions have unmet requirements. Inspect their dependencies, missing evidence and verification requirements; human review can still be needed."
			default:
				context.Status = "evidence_unavailable"
				context.CoverageIncomplete = true
			}
		}
	}
	out.ObligationContext = &context
	out.ObligationAssessments = []api.OperationWorkflowObligationAssessment{}
	for _, action := range out.NextActions {
		assessment := api.OperationWorkflowObligationAssessment{SubscriptionID: action.SubscriptionID, Sequence: action.Sequence, ActionKind: action.Kind, Status: "fulfillment_unassessed", Summary: "This obligation contains a human plan without a declared binding to a workflow transition. Review the plan and source controls; transition readiness alone cannot establish that the work can be fulfilled.", CheckedAt: out.CoordinationCheckedAt}
		if action.Obligation != nil {
			assessment.ObligationID = action.Obligation.ID
		}
		switch action.Kind {
		case "fulfillment_review":
			assessment.Status = "review_actionable"
			assessment.Summary = "Review the saved fulfillment evidence and explicitly reaffirm or reopen it. The original obligation remains fulfilled."
		case "handoff_acceptance":
			assessment.Status = "acceptance_required"
			assessment.Summary = "The handoff is awaiting explicit human acceptance or decline. Inspect its source controls; workflow prerequisites do not establish ownership acceptance."
		case "scheduled_evidence_check":
			assessment.Status = "scheduled_check"
			assessment.Summary = "A worker evidence check is scheduled. Inspect the source to reschedule or cancel; an overdue deadline does not prove the check ran."
		case "decision_revalidation", "coordinator_evidence_review", "coordinator_revision", "dependency_review", "follow_up":
			if fresh {
				assessment.Status = "review_actionable"
				assessment.Summary = "A current application report is available for coordination review. Inspect linked evidence and source controls; workflow requirements are context, not permission to execute."
			} else {
				assessment.Status = "review_evidence_unavailable"
				assessment.Summary = "A current application report is missing or stale. Obtain fresh evidence for review; retained deadlines and source controls remain visible."
			}
		case "handoff_reassignment", "escalation_acknowledgment":
			assessment.Status = "human_action_required"
			assessment.Summary = "A human routing or acknowledgment decision is outstanding. Use source controls to record responsibility; application execution remains separately authorized."
		}
		for _, row := range rows {
			if row.SubscriptionID != action.SubscriptionID || row.Sequence != action.Sequence {
				continue
			}
			for _, binding := range row.Coordination.TargetBindings {
				if action.Obligation == nil || binding.ObligationID != action.Obligation.ID || binding.Case.Subject != out.Timeline.Subject || binding.Case.Workflow != out.Timeline.Workflow || binding.Case.InstanceID != out.Timeline.InstanceID || operator && !sameOperationHistoryIdentity(binding.Case.PlatformTenantID, tenant) {
					continue
				}
				value := binding
				if !operator {
					value.Case.PlatformTenantID = ""
				}
				assessment.Binding = &value
				assessment.Status = "target_unavailable"
				assessment.Summary = "The bound transition cannot be assessed against a fresh current declaration. Refresh evidence or revise the binding; old readiness is not carried forward."
				if fresh && !out.Instance.State.Terminal && out.Instance.Readiness != nil {
					for _, edge := range out.Instance.Readiness.Items {
						target := binding.Transition
						if edge.Declared && edge.ContractVersion == target.ContractVersion && edge.Transition.Operation == target.Operation && edge.Transition.From == target.From && edge.Transition.To == target.To {
							current := edge
							assessment.Readiness = &current
							assessment.CheckedAt = out.EvidenceCheckedAt
							assessment.Status = "waiting_on_target_requirements"
							assessment.Summary = "The explicitly bound transition has unmet evaluated prerequisites. Inspect its current evidence and dependency cases; the deadline remains independent."
							if edge.Ready {
								assessment.Status = "target_requirements_satisfied"
								assessment.Summary = "The explicitly bound transition satisfies its evaluated prerequisites. The application must still authorize execution; obligation fulfillment is not established."
							}
						}
					}
				}
			}
		}
		out.ObligationAssessments = append(out.ObligationAssessments, assessment)
	}
	out.Limitations = append(out.Limitations, "Obligation assessments describe coordination and current transition context separately. Free-form plans have no inferred dependency bindings; overdue deadlines, lineage and readiness never grant execution permission.")
}
