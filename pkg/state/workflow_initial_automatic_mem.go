package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) initialAutomaticChargesLocked(app string) []time.Time {
	var out []time.Time
	for _, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app) && r.Policy.Mode == "automatic_initial" && r.SubmittedAt != nil {
			out = append(out, *r.SubmittedAt)
		}
	}
	return out
}
func (m *MemStore) initialAutomaticOutstandingLocked(app string) *api.WorkflowInitialRecoveryReview {
	var chosen *api.WorkflowInitialRecoveryReview
	for _, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app) && r.Policy.Mode == "automatic_initial" && initialReviewUnresolved(r) && (chosen == nil || r.SubmittedAt.Before(*chosen.SubmittedAt) || r.SubmittedAt.Equal(*chosen.SubmittedAt) && r.ID < chosen.ID) {
			copied := recoveryPolicyClone(r)
			chosen = &copied
		}
	}
	return chosen
}
func (m *MemStore) initialAutomaticSubmissionReasonLocked(app string, p api.WorkflowRecoveryPolicyResponse, now time.Time) string {
	hourly := 0
	var last *time.Time
	charges := m.initialAutomaticChargesLocked(app)
	if stored := m.recoveryPolicies[canonicalMemUUID(app)]; stored != nil {
		for _, at := range stored.Charges {
			charges = append(charges, at)
		}
	}
	for _, at := range charges {
		if at.After(now.Add(-time.Hour)) {
			hourly++
		}
		if last == nil || at.After(*last) {
			copied := at
			last = &copied
		}
	}
	return initialAutomaticPressureReason(p, hourly, last, m.initialAutomaticUnresolvedLocked(app, now), now)
}
func (m *MemStore) initialAutomaticUnresolvedLocked(app string, now time.Time) bool {
	unresolved := m.initialAutomaticOutstandingLocked(app) != nil
	if stored := m.recoveryPolicies[canonicalMemUUID(app)]; stored != nil {
		for _, e := range stored.Executions {
			if e.Status == "applied" && e.Result == nil {
				unresolved = true
			}
		}
	}
	for _, j := range m.eventRecoveryJobs {
		if !sameMemUUID(j.Job.AppID, app) || j.Job.Selection.WorkflowRuns == nil {
			continue
		}
		for _, item := range j.Items {
			if item.State == "pending" && eventRecoveryActive(j.Job.State) {
				unresolved = true
			}
			if item.State == "queued" {
				switch m.workflowRecoveryExecutionLocked(j, item, now).State {
				case "succeeded", "failed", "cancelled":
				default:
					unresolved = true
				}
			}
		}
	}
	for _, run := range m.workflowRuns {
		if sameMemUUID(run.AppID, app) && run.ResumeCount > 0 && (run.Status == WorkflowRunStatusPending || run.Status == WorkflowRunStatusRunning || run.Status == WorkflowRunStatusAwaitingEvent) {
			unresolved = true
		}
	}
	return unresolved
}
func (m *MemStore) processInitialAutomaticResultLocked(ctx context.Context, p *memWorkflowRecoveryPolicy, app App, r api.WorkflowInitialRecoveryReview, now time.Time) error {
	reason := "evidence_unknown"
	var result *api.WorkflowRecoveryReviewActionStatus
	if job := m.eventRecoveryJobs[r.JobID]; job != nil && len(job.Items) == 1 {
		item := job.Items[0]
		if item.WorkflowRunID == r.Target.RunID && item.ExpectedResumeCount != nil && *item.ExpectedResumeCount == 0 {
			result, reason = initialAutomaticOutcome(r, job.Job, item.EventRecoveryItem, m.workflowRecoveryExecutionLocked(job, item, now), now)
			if result == nil && job.Job.State == "paused" {
				reason = "job_paused"
			}
		}
	}
	initialAutomaticEvaluation(&r, reason, now)
	if result != nil {
		r.AutomaticResult = result
		initialReviewTransition(ctx, &r, "submitted", "result_recorded", now)
		m.recordInitialLifecycleLocked(r)
		if r.PolicyVersion == p.Response.Version && p.Response.Policy.Mode == "automatic_initial" && recoveryAutomaticRecordFailure(&p.Response, *result, now) {
			if err := m.enqueueAutomaticRecoveryPauseLocked(app.AccountID, app.Slug, p.Response); err != nil {
				return err
			}
		}
	}
	if err := m.captureInitialAutomaticBlockLocked(app.AccountID, &r, reason, now); err != nil {
		return err
	}
	m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
	return nil
}
func (m *MemStore) executeInitialAutomaticPendingLocked(ctx context.Context, p *memWorkflowRecoveryPolicy, app App, now time.Time) error {
	var chosen *api.WorkflowInitialRecoveryReview
	for _, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app.ID) && r.PolicyVersion == p.Response.Version && r.Policy.Mode == "automatic_initial" && r.Status == "prepared" && (chosen == nil || r.PreparedAt.Before(chosen.PreparedAt) || r.PreparedAt.Equal(chosen.PreparedAt) && r.ID < chosen.ID) {
			copied := recoveryPolicyClone(r)
			chosen = &copied
		}
	}
	if chosen == nil {
		return nil
	}
	r := *chosen
	submitted, err := m.decideInitialReviewLocked(ctx, app.AccountID, app.ID, r.ID, p.Response, api.WorkflowInitialRecoveryReviewDecisionRequest{Decision: "submit", Reason: p.Response.Policy.Reason}, now)
	reason := "child_created"
	if err != nil {
		var blocked bool
		reason, blocked = initialAutomaticBlockedReason(err)
		if !blocked {
			return err
		}
	} else {
		r = submitted
		if r.Status != "submitted" {
			reason = r.Reason
		}
	}
	initialAutomaticEvaluation(&r, reason, now)
	if err := m.captureInitialAutomaticBlockLocked(app.AccountID, &r, reason, now); err != nil {
		return err
	}
	m.initialRecoveryReviews[r.ID] = recoveryPolicyClone(r)
	return nil
}
