package state

import (
	"slices"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Project current source records, never immutable delivery snapshots, before case page truncation.
func projectWorkflowCaseNextActions(out *api.OperationWorkflowCase, rows []api.OperationWorkflowReadinessFollowUp, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool) {
	projectWorkflowCaseNextActionsWithLimit(out, rows, tenant, opts, operator, 100)
}
func projectWorkflowCaseNextActionsWithLimit(out *api.OperationWorkflowCase, rows []api.OperationWorkflowReadinessFollowUp, tenant string, opts api.OperationWorkflowCoordinationTimelineOptions, operator bool, limit int) {
	actions := []api.OperationWorkflowCaseNextAction{}
	match := func(ref api.OperationWorkflowReviewResponseLink) bool {
		return ref.Subject == out.Timeline.Subject && ref.Workflow == opts.Workflow && ref.InstanceID == opts.InstanceID && (!operator || sameOperationHistoryIdentity(ref.PlatformTenantID, tenant))
	}
	refsForCase := func(refs []api.OperationWorkflowReviewResponseLink) []api.OperationWorkflowReviewResponseLink {
		selected := []api.OperationWorkflowReviewResponseLink{}
		for _, ref := range refs {
			if match(ref) {
				copy := ref
				if !operator {
					copy.PlatformTenantID = ""
				}
				selected = append(selected, copy)
			}
		}
		return selected
	}
	for _, row := range rows {
		if row.Scope == opts.Scope {
			actions = append(actions, projectFulfillmentReviewActions(row, out.Timeline.Subject, tenant, opts, operator, out.CoordinationCheckedAt)...)
		}
		if row.Scope != opts.Scope || row.Coordination.Completion != nil || row.Coordination.FollowUpAt == nil {
			continue
		}
		direct := row.Subject != nil && *row.Subject == out.Timeline.Subject && row.Workflow == opts.Workflow && row.InstanceID == opts.InstanceID && (!operator || sameOperationHistoryIdentity(row.PlatformTenantID, tenant))
		add := func(kind, summary, owner, trigger string, due *time.Time, evidence []api.OperationWorkflowReviewResponseLink, shared bool) {
			obligation := workflowActionObligation(row, kind)
			c := api.OperationWorkflowNextActionCase{Subject: out.Timeline.Subject, Workflow: opts.Workflow, InstanceID: opts.InstanceID, PlatformTenantID: tenant}
			sourceTenant := row.SubscriptionTenantID
			if !operator {
				sourceTenant = tenant
			}
			if obligation != nil && obligationFulfilled(row.Coordination, obligation.ID, c, sourceTenant) {
				return
			}
			actions = append(actions, api.OperationWorkflowCaseNextAction{Obligation: obligation, Kind: kind, Summary: summary, Owner: owner, Trigger: trigger, DueAt: due, Overdue: due != nil && !due.After(out.CoordinationCheckedAt), SubscriptionID: row.SubscriptionID, Sequence: row.Sequence, Evidence: evidence, SharedPlan: shared})
		}
		response := row.ReviewResponse
		if row.Kind == "business_impact_changed" && response != nil && response.Status == "active" {
			evidence := refsForCase(response.Reviews)
			if len(evidence) == 0 {
				continue
			}
			shared := len(evidence) != len(response.Reviews)
			add("response_check_in", response.Plan, response.Owner, "human", response.CheckInAt, evidence, shared)
			review := response.DecisionReview
			if review == nil || response.Decision == nil || !review.DecisionAt.Equal(response.Decision.At) {
				continue
			}
			due := review.DueAt
			if due == nil {
				value := review.RequiredAt.Add(time.Duration(effectiveDecisionReviewWindow(response)) * time.Second)
				due = &value
			}
			add("decision_revalidation", "Review current conditions and record continue, revise or pause.", response.Owner, "human", due, evidence, shared)
			owned := review.Owner != "" && response.Owner == review.Owner && row.Coordination.Owner == response.Owner
			if !owned {
				continue
			}
			if review.Decline != nil && review.ReassignmentRequired {
				add("handoff_reassignment", "Route the declined handoff to a different team with a reason.", response.Owner, "human", nil, evidence, shared)
			} else if review.Reassigned && review.Acceptance == nil && review.Decline == nil {
				pinned := pinnedDecisionReview(response)
				add("handoff_acceptance", "Explicitly accept the handoff with a note and future check-in.", response.Owner, "human", pinned.AcceptanceDueAt, evidence, shared)
				if review.AcceptanceEscalatedAt != nil && review.EscalationAcknowledgment == nil {
					add("escalation_acknowledgment", "Name an escalation coordinator, action plan and next-action deadline.", review.AcceptanceEscalationOwner, "human", nil, evidence, shared)
				}
			}
			ack := review.EscalationAcknowledgment
			if ack == nil {
				continue
			}
			if ack.Completion == nil {
				add("coordinator_completion", ack.Plan, ack.Coordinator, "human", &ack.DueAt, evidence, shared)
				continue
			}
			completion := ack.Completion
			selected := refsForCase(completion.Evidence)
			if len(selected) == 0 {
				continue
			}
			shared = len(selected) != len(completion.Evidence)
			if completion.ReviewDueAt != nil && completion.ScheduledReviewAt == nil && completion.ReviewCancellation == nil {
				add("scheduled_evidence_check", "A scheduled worker check will observe completion evidence. Open the source to reschedule or cancel.", ack.Coordinator, "automatic", completion.ReviewDueAt, selected, shared)
			} else if completion.Review == nil {
				add("coordinator_evidence_review", "Review current evidence for the completed coordinator action.", ack.Coordinator, "suggested", nil, selected, shared)
			}
			if completion.Review != nil && completion.Review.Health.Status != "clear" && review.Acceptance == nil && review.Decline == nil {
				add("coordinator_revision", "Consider a revised coordinator plan; evidence is rechecked when saving.", ack.Coordinator, "suggested", nil, selected, shared)
			}
			continue
		}
		if !direct {
			continue
		}
		if row.PlaybookStep != nil && row.PlaybookStep.DependencyReview != nil && row.PlaybookStep.DependencyReviewStatus == "awaiting" {
			due := row.Coordination.FollowUpAt
			if row.PlaybookStep.Response != nil {
				due = &row.PlaybookStep.Response.DeadlineAt
			}
			add("dependency_review", "Review the changed prerequisite evidence for this playbook step.", row.Coordination.Owner, "human", due, nil, false)
		} else {
			summary := row.Coordination.Note
			if summary == "" {
				summary = "Review the retained follow-up with current application evidence."
			}
			add("follow_up", summary, row.Coordination.Owner, "human", row.Coordination.FollowUpAt, nil, false)
		}
	}
	sort.Slice(actions, func(i, j int) bool {
		a, b := actions[i], actions[j]
		if a.Overdue != b.Overdue {
			return a.Overdue
		}
		if (a.DueAt == nil) != (b.DueAt == nil) {
			return a.DueAt != nil
		}
		if a.DueAt != nil && !a.DueAt.Equal(*b.DueAt) {
			return a.DueAt.Before(*b.DueAt)
		}
		if a.SubscriptionID != b.SubscriptionID {
			return a.SubscriptionID < b.SubscriptionID
		}
		if a.Sequence != b.Sequence {
			return a.Sequence < b.Sequence
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Obligation != nil && b.Obligation != nil {
			return a.Obligation.ID < b.Obligation.ID
		}
		return false
	})
	out.NextActionCount = len(actions)
	out.NextActionsTruncated = len(actions) > limit
	if out.NextActionsTruncated {
		actions = actions[:limit]
	}
	out.NextActions = slices.Clone(actions)
	if out.NextActions == nil {
		out.NextActions = []api.OperationWorkflowCaseNextAction{}
	}
}
