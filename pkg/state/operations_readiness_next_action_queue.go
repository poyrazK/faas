package state

import (
	"context"
	"slices"
	"sort"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

var workflowNextActionKinds = []string{"response_check_in", "decision_revalidation", "handoff_acceptance", "handoff_reassignment", "escalation_acknowledgment", "coordinator_completion", "scheduled_evidence_check", "coordinator_evidence_review", "coordinator_revision", "dependency_review", "follow_up", "fulfillment_review"}

func readinessQueueOffsetMax(view string) int {
	if view == "next_actions" {
		return api.OperationWorkflowReadinessFollowUpCandidatesMax * (len(workflowNextActionKinds) - 1 + 128)
	}
	return api.OperationWorkflowReadinessFollowUpCandidatesMax
}

func nextActionWorkQueue(ctx context.Context, rows []api.OperationWorkflowReadinessFollowUp, tenant string, opts api.OperationWorkflowReadinessFollowUpOptions, operator bool, limit int, out api.OperationWorkflowReadinessFollowUpsResponse) (api.OperationWorkflowReadinessFollowUpsResponse, error) {
	matched := []api.OperationWorkflowNextActionQueueItem{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		cases := []api.OperationWorkflowNextActionCase{}
		addCase := func(subject api.OperationSubject, workflow, instance, customer string) {
			if workflow == "" || instance == "" || opts.Workflow != "" && workflow != opts.Workflow || operator && tenant != "" && !sameOperationHistoryIdentity(customer, tenant) {
				return
			}
			c := api.OperationWorkflowNextActionCase{Subject: subject, Workflow: workflow, InstanceID: instance, PlatformTenantID: customer}
			if !slices.Contains(cases, c) {
				cases = append(cases, c)
			}
		}
		if row.Kind == "business_impact_changed" && row.ReviewResponse != nil {
			for _, ref := range row.ReviewResponse.Reviews {
				addCase(ref.Subject, ref.Workflow, ref.InstanceID, ref.PlatformTenantID)
			}
		} else if row.Subject != nil {
			addCase(*row.Subject, row.Workflow, row.InstanceID, row.PlatformTenantID)
		}
		for _, review := range row.Coordination.FulfillmentReviews {
			if review.ReviewRequired {
				c := review.Binding.Case
				customer := c.PlatformTenantID
				if customer == "" {
					customer = row.PlatformTenantID
				}
				addCase(c.Subject, c.Workflow, c.InstanceID, customer)
			}
		}
		byKind := map[string]int{}
		for _, c := range cases {
			caseOpts := api.OperationWorkflowCoordinationTimelineOptions{Scope: opts.Scope, SubjectType: c.Subject.Type, SubjectID: c.Subject.ID, Workflow: c.Workflow, InstanceID: c.InstanceID}
			projected := api.OperationWorkflowCase{CoordinationCheckedAt: out.EvaluatedAt, Timeline: api.OperationWorkflowCoordinationTimeline{Subject: c.Subject}}
			projectWorkflowCaseNextActionsWithLimit(&projected, []api.OperationWorkflowReadinessFollowUp{row}, c.PlatformTenantID, caseOpts, operator, readinessQueueOffsetMax("next_actions"))
			for _, action := range projected.NextActions {
				if opts.Owner != "" && action.Owner != opts.Owner || opts.ActionKind != "" && action.Kind != opts.ActionKind || opts.ActionTrigger != "" && action.Trigger != opts.ActionTrigger || opts.Due == "overdue" && !action.Overdue || opts.Due == "upcoming" && (action.DueAt == nil || action.Overdue) {
					continue
				}
				shown := c
				if !operator {
					shown.PlatformTenantID = ""
				}
				identity := action.Kind
				if action.Kind == "fulfillment_review" && action.Obligation != nil {
					identity += "/" + action.Obligation.ID
				}
				if index, exists := byKind[identity]; exists {
					item := &matched[index]
					item.Cases = append(item.Cases, shown)
					for _, ref := range action.Evidence {
						if !slices.Contains(item.Action.Evidence, ref) {
							item.Action.Evidence = append(item.Action.Evidence, ref)
						}
					}
					item.Action.SharedPlan = true
				} else {
					item := api.OperationWorkflowNextActionQueueItem{Action: action, Cases: []api.OperationWorkflowNextActionCase{shown}, Scope: row.Scope, SubscriptionTenantID: row.SubscriptionTenantID}
					if !operator {
						item.SubscriptionTenantID = ""
					}
					byKind[identity] = len(matched)
					matched = append(matched, item)
				}
			}
		}
	}
	key := func(c api.OperationWorkflowNextActionCase) string {
		return c.PlatformTenantID + "\x00" + c.Subject.Type + "\x00" + c.Subject.ID + "\x00" + c.Workflow + "\x00" + c.InstanceID
	}
	for i := range matched {
		sort.Slice(matched[i].Cases, func(a, b int) bool { return key(matched[i].Cases[a]) < key(matched[i].Cases[b]) })
		sort.Slice(matched[i].Action.Evidence, func(a, b int) bool {
			x, y := matched[i].Action.Evidence[a], matched[i].Action.Evidence[b]
			return x.SubscriptionID+"/"+strconv.FormatInt(x.Sequence, 10) < y.SubscriptionID+"/"+strconv.FormatInt(y.Sequence, 10)
		})
	}
	sort.Slice(matched, func(i, j int) bool {
		a, b := matched[i].Action, matched[j].Action
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
	out.View = "next_actions"
	out.TotalCount = len(matched)
	for _, item := range matched {
		if item.Action.Overdue {
			out.OverdueCount++
		}
	}
	start := min(opts.Offset, len(matched))
	end := min(start+limit, len(matched))
	out.NextActions = matched[start:end]
	out.NextOffset = end
	out.HasMore = end < len(matched)
	return out, nil
}
