package state

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// These are disposition/completion observations. Freshness requires the existing status inspector.
func (m *MemStore) recoveryOverviewRetryLocked(ctx context.Context, p *memWorkflowRecoveryPolicy, q api.WorkflowRecoveryAutomaticQueue, manual *recoveryOverviewManual, scope App) error {
	for _, raw := range p.Reviews {
		if err := ctx.Err(); err != nil {
			return err
		}
		var r api.WorkflowRecoveryPolicyReview
		if err := json.Unmarshal(raw, &r); err != nil {
			return err
		}
		if r.Policy.Mode != "preview" || r.Plan == nil || len(r.Plan.Actions) == 0 || r.ObservedAt.After(q.ObservedAt) || m.eventRecoveryJobs[r.JobID] == nil {
			continue
		}
		job := m.eventRecoveryJobs[r.JobID]
		if !sameMemUUID(job.Job.AppID, scope.ID) || !sameMemUUID(job.AccountID, scope.AccountID) {
			continue
		}
		hasRetry := false
		for _, action := range r.Plan.Actions {
			hasRetry = hasRetry || action.Kind == "retry"
		}
		if !hasRetry {
			continue
		}
		n := p.Notifications[r.ID]
		if r.CompletionNotification != nil || n != nil && n.Completion != nil {
			continue
		}
		reason := ""
		if r.ReviewDecision != nil {
			if r.ReviewDecision.Decision == "approved" {
				reason = "awaiting_completion_observation"
				manual.RetryUnresolved++
			}
		} else if q.Policy.Policy.Enabled && r.PolicyVersion == q.Policy.Version {
			if n != nil && n.Receipt != nil && n.Receipt.Status == "suppressed" {
				continue
			}
			reason = "review_inspection_required"
			manual.RetryAwaiting++
		}
		if reason == "" {
			continue
		}
		manual.RetryIssues = append(manual.RetryIssues, api.WorkflowRecoveryOverviewIssue{Scope: "manual_retry", Reason: reason, PolicyVersion: r.PolicyVersion, ReviewID: r.ID, JobID: r.JobID, InspectionURL: "/v1/apps/" + url.PathEscape(scope.Slug) + "/recovery-policy/reviews/" + url.PathEscape(r.ID) + "/status"})
		sort.Slice(manual.RetryIssues, func(i, j int) bool { return manual.RetryIssues[i].ReviewID < manual.RetryIssues[j].ReviewID })
		if len(manual.RetryIssues) > api.WorkflowRecoveryOverviewItemsMax {
			manual.RetryIssues = manual.RetryIssues[:api.WorkflowRecoveryOverviewItemsMax]
		}
	}
	return nil
}
