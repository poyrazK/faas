package state

import "github.com/onebox-faas/faas/pkg/api"

func summarizeWorkflowCase(out *api.OperationWorkflowCase) {
	add := func(a api.OperationWorkflowCaseAction) {
		if len(out.Actions) >= api.OperationWorkflowCaseActionsMax {
			out.ActionsTruncated = true
			return
		}
		out.Actions = append(out.Actions, a)
	}
	out.Status = "unknown"
	out.Summary = "No retained application state is available for this exact workflow case."
	instance := out.Instance
	if instance == nil || instance.State == nil {
		add(api.OperationWorkflowCaseAction{Kind: "refresh_evidence", Summary: "Ask the application to publish current workflow evidence; retained coordination cannot establish current business state."})
	} else {
		state := instance.State
		switch {
		case state.Stale:
			out.Status = "stale"
			out.Summary = "The latest application report is stale; current readiness and business outcome are unknown."
			add(api.OperationWorkflowCaseAction{Kind: "refresh_evidence", Summary: "Obtain fresh application-reported state before assessing business outcome or transition readiness.", OperationID: state.OperationID})
		case state.Terminal:
			out.Status = "terminal_reported"
			out.Summary = "The application reports a terminal workflow. Interpret its explicit outcome code using application business rules; retained follow-ups still require human review."
		default:
			out.Status = "no_transition"
			out.Summary = "No declared current-state transition is available in the retained projection."
			if instance.Readiness != nil {
				if instance.Readiness.HasMore {
					out.Limitations = append(out.Limitations, "Current transition readiness is truncated; omitted transitions cannot be assessed here.")
				}
				if len(instance.Readiness.Items) > 0 {
					out.Status = "blocked"
					out.Summary = "Declared current-state transitions have unmet requirements in the retained application evidence."
				}
				for _, r := range instance.Readiness.Items {
					summary := "Review unmet prerequisites for " + r.Transition.Operation + " (" + r.Transition.From + " → " + r.Transition.To + ")."
					kind := "review_prerequisites"
					if r.Ready {
						out.Status = "ready"
						out.Summary = "At least one declared current-state transition satisfies the evaluated requirements; the application must still authorize execution."
						kind = "request_application_authorization"
						summary = "Ask the application to authorize " + r.Transition.Operation + " (" + r.Transition.From + " → " + r.Transition.To + "); this view does not execute it."
					}
					add(api.OperationWorkflowCaseAction{Kind: kind, Summary: summary, OperationID: state.OperationID})
				}
			}
			for _, b := range state.Blockers {
				summary := b.NextAction
				if summary == "" {
					summary = "Review application blocker " + b.Code + ": " + b.Description
				}
				add(api.OperationWorkflowCaseAction{Kind: "review_blocker", Summary: summary, Owner: b.Owner, OperationID: state.OperationID})
			}
			for _, proof := range instance.ResolutionVerifications {
				if proof.Status == "awaiting_verification" {
					add(api.OperationWorkflowCaseAction{Kind: "obtain_verification", Summary: "Obtain the application's named verification for blocker " + proof.Resolution.Code + ".", Owner: proof.Resolution.VerificationOwner, OperationID: proof.ResolutionOperationID})
				}
			}
		}
	}
	for _, row := range out.FollowUps {
		summary := "Review retained follow-up with current application evidence before completing coordination."
		if row.Overdue {
			summary = "Review overdue follow-up with current application evidence before completing coordination."
		}
		add(api.OperationWorkflowCaseAction{Kind: "review_follow_up", Summary: summary, Owner: row.Coordination.Owner, SubscriptionID: row.SubscriptionID, Sequence: row.Sequence})
	}
}
