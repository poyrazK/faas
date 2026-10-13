package state

import (
	"context"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const impactWatchSourcesMax = 64

type impactReviewSource struct {
	TenantID    string               `json:"tenant_id"`
	Subject     api.OperationSubject `json:"subject"`
	Workflow    string               `json:"workflow"`
	InstanceID  string               `json:"instance_id"`
	ProgressID  string               `json:"progress_id"`
	OutcomeCode string               `json:"outcome_code"`
	ItemIndex   int                  `json:"item_index"`
	Revision    int64                `json:"revision"`
}

func validateImpactWatchRequests(watches []api.OperationWorkflowReadinessImpactWatchRequest) error {
	if len(watches) > 16 {
		return ErrInvalidArgument
	}
	seen := map[impactSummaryKey]bool{}
	for _, watch := range watches {
		if watch.AcknowledgeAfterSeconds < 0 || watch.AcknowledgeAfterSeconds > api.OperationWorkflowReadinessOutcomeEscalationDelayMaxSeconds || (watch.AcknowledgeAfterSeconds > 0 && watch.Owner == "") || (watch.FallbackOwner != "" && (watch.AcknowledgeAfterSeconds == 0 || watch.FallbackOwner == watch.Owner || len(watch.FallbackOwner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(watch.FallbackOwner) || strings.TrimSpace(watch.FallbackOwner) != watch.FallbackOwner || strings.IndexFunc(watch.FallbackOwner, unicode.IsControl) >= 0)) {
			return ErrInvalidArgument
		}
		key := impactSummaryKey{watch.Workflow, watch.Kind, watch.Code}
		if seen[key] || api.ValidateOperationWorkflowName(watch.Workflow) != nil || !slices.Contains([]string{"milestone", "outcome"}, watch.Kind) || api.ValidateOperationWorkflowStateName(watch.Code) != nil || len(watch.Owner) > api.OperationWorkflowReadinessFollowUpOwnerMaxBytes || !utf8.ValidString(watch.Owner) || strings.TrimSpace(watch.Owner) != watch.Owner || strings.IndexFunc(watch.Owner, unicode.IsControl) >= 0 {
			return ErrInvalidArgument
		}
		seen[key] = true
	}
	return nil
}

func evaluateReadinessImpactWatches(ctx context.Context, store readinessSubscriptionBackend, r *readinessSubscriptionRecord, initial bool) ([]api.OperationWorkflowReadinessNotification, error) {
	notices := []api.OperationWorkflowReadinessNotification{}
	if len(r.Subscription.BusinessImpactWatches) == 0 {
		return notices, nil
	}
	rows, err := store.readReadinessFollowUps(ctx, r.Account, r.Tenant, r.Subscription.AppID, r.Subscription.Scope, r.Operator)
	if err != nil {
		return nil, err
	}
	checked := time.Now().UTC().Truncate(time.Microsecond)
	scoped := []api.OperationWorkflowReadinessFollowUp{}
	truncated := len(rows) >= api.OperationWorkflowReadinessFollowUpCandidatesMax
	for _, row := range rows {
		if r.Operator && r.Subscription.TenantID != "" && !sameOperationHistoryIdentity(row.PlatformTenantID, r.Subscription.TenantID) {
			continue
		}
		if row.Coordination.FollowUpAt == nil {
			continue
		}
		row.Overdue = !row.Coordination.FollowUpAt.After(checked)
		scoped = append(scoped, row)
	}
	summaries := readinessBusinessImpactSummary(scoped)
	for len(r.ImpactSources) < len(r.Subscription.BusinessImpactWatches) {
		r.ImpactSources = append(r.ImpactSources, nil)
	}
	for len(r.ImpactTruncated) < len(r.Subscription.BusinessImpactWatches) {
		r.ImpactTruncated = append(r.ImpactTruncated, false)
	}
	var firstErr error
	for index := range r.Subscription.BusinessImpactWatches {
		watch := &r.Subscription.BusinessImpactWatches[index]
		var current *api.OperationWorkflowReadinessImpactSummary
		for _, summary := range summaries {
			if summary.Workflow == watch.Workflow && summary.Kind == watch.Kind && summary.Code == watch.Code {
				value := summary
				current = &value
				break
			}
		}
		r.ImpactTruncated[index] = r.ImpactTruncated[index] || truncated
		for _, row := range scoped {
			impact := readinessFollowUpImpact(row)
			if impact == nil || row.Workflow != watch.Workflow || impact.Kind != watch.Kind || impact.Code != watch.Code {
				continue
			}
			if row.Subject == nil {
				r.ImpactTruncated[index] = true
				continue
			}
			step := row.PlaybookStep
			source := impactReviewSource{TenantID: row.PlatformTenantID, Subject: *row.Subject, Workflow: row.Workflow, InstanceID: row.InstanceID, ProgressID: step.ProgressID, OutcomeCode: step.OutcomeCode, ItemIndex: step.ItemIndex, Revision: step.DependencyReview.DependencyReviewRevision}
			if slices.ContainsFunc(r.ImpactSources[index], func(old impactReviewSource) bool {
				return old.ProgressID == source.ProgressID && old.OutcomeCode == source.OutcomeCode && old.ItemIndex == source.ItemIndex && old.Revision == source.Revision
			}) {
				continue
			}
			if len(r.ImpactSources[index]) >= impactWatchSourcesMax {
				r.ImpactTruncated[index] = true
				continue
			}
			r.ImpactSources[index] = append(r.ImpactSources[index], source)
		}
		watch.CoverageIncomplete = r.ImpactTruncated[index]
		if !initial && current == nil && watch.Current != nil {
			cleared := false
			if !r.ImpactTruncated[index] {
				cleared, err = impactWatchSourcesCleared(ctx, store, *r, *watch, r.ImpactSources[index])
				if err != nil && firstErr == nil {
					firstErr = err
				}
			}
			if !cleared {
				watch.CoverageIncomplete = true
				continue
			}
		}
		reasons := []string{}
		if !initial {
			beforeCritical, beforeOverdue := 0, 0
			if watch.Current != nil {
				beforeCritical = watch.Current.CriticalCount
				beforeOverdue = watch.Current.OverdueCount
			}
			if current != nil && beforeCritical == 0 && current.CriticalCount > 0 {
				reasons = append(reasons, "critical_review_added")
			}
			if current != nil && beforeOverdue == 0 && current.OverdueCount > 0 {
				reasons = append(reasons, "review_became_overdue")
			}
			if current == nil && watch.Current != nil {
				reasons = append(reasons, "reviews_cleared")
			}
		}
		if len(reasons) > 0 {
			n := readinessSubscriptionNotify(r, "business_impact_changed", "Review confidence for a protected business result changed. Inspect the matching review queue; application business execution and reported outcomes are unchanged.", nil)
			n.BusinessImpact = &api.OperationWorkflowReadinessImpactEvent{Workflow: watch.Workflow, Kind: watch.Kind, Code: watch.Code, CheckedAt: checked, Reasons: reasons, Previous: watch.Current, Current: current}
			n.Coordination = &api.OperationWorkflowReadinessCoordination{Revision: 1, Status: "unread", Owner: watch.Owner, UpdatedAt: n.CreatedAt, UpdatedBy: "system:business-impact-watch"}
			if watch.AcknowledgeAfterSeconds > 0 && !slices.Contains(reasons, "reviews_cleared") {
				deadline := n.CreatedAt.Add(time.Duration(watch.AcknowledgeAfterSeconds) * time.Second)
				n.BusinessImpact.Response = &api.OperationWorkflowReadinessEscalationResponse{DeadlineAt: deadline, InitialOwner: watch.Owner, FallbackOwner: watch.FallbackOwner, Status: "awaiting", History: []api.OperationWorkflowReadinessAssignmentChange{}}
				n.Coordination.FollowUpAt = &deadline
			}
			notices = append(notices, n)
		}
		watch.Current = current
		watch.EvaluatedAt = checked
		if current == nil && !truncated {
			r.ImpactSources[index] = nil
			r.ImpactTruncated[index] = false
			watch.CoverageIncomplete = false
		}
	}
	return notices, firstErr
}

// Queue disappearance is insufficient: retained source checklist decisions must
// resolve every tracked obligation. Missing progress or ongoing reviews stay unknown.
func impactWatchSourcesCleared(ctx context.Context, store readinessSubscriptionBackend, r readinessSubscriptionRecord, watch api.OperationWorkflowReadinessImpactWatch, sources []impactReviewSource) (bool, error) {
	if len(sources) == 0 {
		return false, nil
	}
	cache := map[string][]api.OperationWorkflowPlaybookProgress{}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		identity := playbookEvidenceWatchIdentity(r, api.OperationWorkflowPlaybookEvidenceWatch{PlatformTenantID: source.TenantID, Subject: source.Subject, Workflow: source.Workflow, InstanceID: source.InstanceID})
		rows, exists := cache[identity.key()]
		if !exists {
			var err error
			rows, err = store.readPlaybookProgress(ctx, identity)
			if err != nil {
				return false, err
			}
			cache[identity.key()] = rows
		}
		resolved := false
		for _, p := range rows {
			for _, follow := range p.OutcomeFollowUps {
				for _, item := range follow.Items {
					if item.DependencyReviewRevision > 0 && item.ReviewImpact != nil && item.ReviewImpact.Kind == watch.Kind && item.ReviewImpact.Code == watch.Code {
						return false, nil
					}
				}
			}

			if p.ID != source.ProgressID {
				continue
			}
			for _, follow := range p.OutcomeFollowUps {
				if follow.OutcomeCode == source.OutcomeCode && source.ItemIndex >= 0 && source.ItemIndex < len(follow.Items) {
					resolved = follow.Items[source.ItemIndex].DependencyReviewRevision == 0
				}
			}
		}
		if !resolved {
			return false, nil
		}
	}
	return true, nil
}
