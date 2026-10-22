package state

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

type OperationWorkflowReadinessFollowUpStore interface {
	ListWorkflowReadinessFollowUps(context.Context, string, string, api.OperationWorkflowReadinessFollowUpOptions, bool) (api.OperationWorkflowReadinessFollowUpsResponse, error)
}
type readinessFollowUpBackend interface {
	readReadinessFollowUps(context.Context, string, string, string, string, bool) ([]api.OperationWorkflowReadinessFollowUp, error)
}

func listReadinessFollowUps(ctx context.Context, store readinessFollowUpBackend, account, tenant string, opts api.OperationWorkflowReadinessFollowUpOptions, operator bool) (api.OperationWorkflowReadinessFollowUpsResponse, error) {
	out := api.OperationWorkflowReadinessFollowUpsResponse{Items: []api.OperationWorkflowReadinessFollowUp{}, RetentionLimited: true}
	if !operator && opts.TenantID != "" {
		return out, ErrInvalidArgument
	}
	if operator {
		tenant = opts.TenantID
	}
	normalized, _, err := prepareOperationHistoryQuery(account, tenant, api.OperationListOptions{AppID: opts.AppID, Scope: opts.Scope, Limit: opts.Limit}, operator)
	if err != nil {
		return out, err
	}
	if len(opts.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(opts.Owner) || strings.TrimSpace(opts.Owner) != opts.Owner || strings.IndexFunc(opts.Owner, unicode.IsControl) >= 0 || opts.Workflow != "" && api.ValidateOperationWorkflowName(opts.Workflow) != nil || opts.Offset < 0 || opts.Offset > readinessQueueOffsetMax(opts.View) || opts.Due != "" && opts.Due != "all" && opts.Due != "overdue" && opts.Due != "upcoming" {
		return out, ErrInvalidArgument
	}
	if !slices.Contains([]string{"", "normal", "high", "critical"}, opts.ImpactPriority) || !slices.Contains([]string{"", "milestone", "outcome"}, opts.ImpactKind) || !slices.Contains([]string{"", "due", "business_impact"}, opts.Sort) || (opts.ImpactCode != "" && (opts.ImpactKind == "" || api.ValidateOperationWorkflowStateName(opts.ImpactCode) != nil)) {
		return out, ErrInvalidArgument
	}
	if !slices.Contains([]string{"", "follow_ups", "next_actions"}, opts.View) || !slices.Contains([]string{"", "human", "automatic", "suggested"}, opts.ActionTrigger) || opts.ActionKind != "" && !slices.Contains(workflowNextActionKinds, opts.ActionKind) {
		return out, ErrInvalidArgument
	}
	if opts.View == "next_actions" {
		if opts.ImpactPriority != "" || opts.ImpactKind != "" || opts.ImpactCode != "" || opts.ReminderOnly || opts.Sort == "business_impact" {
			return out, ErrInvalidArgument
		}
	} else if opts.ActionKind != "" || opts.ActionTrigger != "" {
		return out, ErrInvalidArgument
	}
	rows, err := store.readReadinessFollowUps(ctx, account, tenant, normalized.AppID, opts.Scope, operator)
	if err != nil {
		return out, err
	}
	out.EvaluatedAt = time.Now().UTC().Truncate(time.Microsecond)
	if opts.View == "next_actions" {
		return nextActionWorkQueue(ctx, rows, tenant, opts, operator, normalized.Limit, out)
	}
	matched := []api.OperationWorkflowReadinessFollowUp{}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if row.Coordination.FollowUpAt == nil || opts.Owner != "" && row.Coordination.Owner != opts.Owner || opts.Workflow != "" && row.Workflow != opts.Workflow || operator && tenant != "" && !sameOperationHistoryIdentity(row.PlatformTenantID, tenant) {
			continue
		}
		impact := readinessFollowUpImpact(row)
		if (opts.ImpactPriority != "" || opts.ImpactKind != "" || opts.ImpactCode != "") && (impact == nil || opts.ImpactPriority != "" && impact.Priority != opts.ImpactPriority || opts.ImpactKind != "" && impact.Kind != opts.ImpactKind || opts.ImpactCode != "" && impact.Code != opts.ImpactCode) {
			continue
		}
		row.AttentionDueAt = readinessFollowUpAttentionDue(row)
		row.Overdue = !row.AttentionDueAt.After(out.EvaluatedAt)
		if opts.Due == "overdue" && !row.Overdue || opts.Due == "upcoming" && row.Overdue {
			continue
		}
		activeReminder := row.Reminder != nil && row.Reminder.DueAt.Equal(*row.Coordination.FollowUpAt) && !row.Coordination.FollowUpAt.After(out.EvaluatedAt)
		if !activeReminder {
			row.Reminder = nil
		}
		if opts.ReminderOnly && !activeReminder {
			continue
		}
		if activeReminder {
			out.DueReminderCount++
		}
		if !operator {
			row.PlatformTenantID = ""
			row.SubscriptionTenantID = ""
		}
		if row.Overdue {
			out.OverdueCount++
		}
		matched = append(matched, row)
	}
	sort.Slice(matched, func(i, j int) bool {
		a, b := matched[i], matched[j]
		if opts.Sort == "business_impact" {
			if a.Overdue != b.Overdue {
				return a.Overdue
			}
			ap, bp := readinessFollowUpImpactPriority(a), readinessFollowUpImpactPriority(b)
			if ap != bp {
				return ap > bp
			}
		}
		if !a.AttentionDueAt.Equal(*b.AttentionDueAt) {
			return a.AttentionDueAt.Before(*b.AttentionDueAt)
		}
		if a.SubscriptionID != b.SubscriptionID {
			return a.SubscriptionID < b.SubscriptionID
		}
		return a.Sequence < b.Sequence
	})
	out.BusinessImpactSummary = readinessBusinessImpactSummary(matched)
	out.TotalCount = len(matched)
	start := min(opts.Offset, len(matched))
	end := min(start+normalized.Limit, len(matched))
	out.Items = matched[start:end]
	out.NextOffset = end
	out.HasMore = end < len(matched)
	return out, nil
}
func readinessFollowUpProjection(s api.OperationWorkflowReadinessSubscription, tenant string, n api.OperationWorkflowReadinessNotification) api.OperationWorkflowReadinessFollowUp {
	row := api.OperationWorkflowReadinessFollowUp{ReviewResponse: n.ReviewResponse, BusinessImpact: n.BusinessImpact, PlaybookStep: n.PlaybookStep, PlaybookEscalation: n.PlaybookEscalation, OutcomeEscalation: n.OutcomeEscalation, SubscriptionID: s.ID, SubscriptionName: s.Name, SubscriptionTenantID: s.TenantID, PlatformTenantID: tenant, Scope: s.Scope, NotificationID: n.ID, Sequence: n.Sequence, Kind: n.Kind, CreatedAt: n.CreatedAt, Coordination: *n.Coordination, Reminder: n.Reminder}
	if b := readinessNotificationBaseline(n); b != nil {
		row.Workflow = b.Workflow
		row.InstanceID = b.InstanceID
		row.Subject = &b.Subject
		row.Operation = b.Operation
		row.FromState = b.FromState
		row.ToState = b.ToState
		if b.PlatformTenantID != "" {
			row.PlatformTenantID = b.PlatformTenantID
		}
	}
	if event := n.BusinessImpact; event != nil {
		row.Workflow = event.Workflow
	}
	if event := n.PlaybookEvidence; event != nil {
		row.Workflow = event.Workflow
		row.InstanceID = event.InstanceID
		subject := event.Subject
		row.Subject = &subject
		if event.PlatformTenantID != "" {
			row.PlatformTenantID = event.PlatformTenantID
		}
	}
	return row
}

func readinessFollowUpImpact(row api.OperationWorkflowReadinessFollowUp) *api.OperationWorkflowPlaybookReviewImpact {
	if row.PlaybookStep == nil || row.PlaybookStep.DependencyReview == nil || row.PlaybookStep.DependencyReviewStatus != "awaiting" {
		return nil
	}
	return row.PlaybookStep.ReviewImpact
}
func readinessFollowUpImpactPriority(row api.OperationWorkflowReadinessFollowUp) int {
	impact := readinessFollowUpImpact(row)
	if impact == nil {
		return 0
	}
	switch impact.Priority {
	case "critical":
		return 3
	case "high":
		return 2
	case "normal":
		return 1
	}
	return 0
}

// Queue urgency may precede a response's human check-in; keep coordination unchanged.
func readinessFollowUpAttentionDue(row api.OperationWorkflowReadinessFollowUp) *time.Time {
	due := row.Coordination.FollowUpAt
	if row.ReviewResponse != nil && row.ReviewResponse.Status == "active" && row.ReviewResponse.DecisionReview != nil {
		review := pinnedDecisionReview(row.ReviewResponse)
		if review.DueAt != nil && (due == nil || review.DueAt.Before(*due)) {
			due = review.DueAt
		}
	}
	return due
}
