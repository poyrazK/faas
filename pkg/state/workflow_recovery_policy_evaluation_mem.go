package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"sort"
	"time"
)

func (m *MemStore) EvaluateWorkflowRecoveryPolicy(ctx context.Context, account, app string, req api.WorkflowRecoveryPolicyEvaluationRequest) (api.WorkflowRecoveryPolicyEvaluation, error) {
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	c, err := recoveryEvaluationWindow(account, app, req, time.Now().UTC())
	if err != nil {
		return api.WorkflowRecoveryPolicyEvaluation{}, err
	}
	m.mu.Lock()
	current, reviews, executions, err := m.recoveryEvaluationSnapshotLocked(ctx, account, app, req, c)
	var failures recoveryFailureSnapshot
	if err == nil {
		failures, err = m.recoveryFailureSnapshotLocked(ctx, app, req, c)
	}
	m.mu.Unlock()
	if err != nil {
		return api.WorkflowRecoveryPolicyEvaluation{}, err
	}
	return evaluateRecoveryPolicy(ctx, c, current, req, reviews, executions, failures)
}
func (m *MemStore) recoveryEvaluationSnapshotLocked(ctx context.Context, account, app string, req api.WorkflowRecoveryPolicyEvaluationRequest, c recoveryHistoryCursor) (api.WorkflowRecoveryPolicyResponse, []recoveryEvaluationReview, []api.WorkflowRecoveryHistoryEntry, error) {
	current := defaultWorkflowRecoveryPolicy(canonicalMemUUID(app))
	reviews := []recoveryEvaluationReview{}
	executions := []api.WorkflowRecoveryHistoryEntry{}
	if err := ctx.Err(); err != nil {
		return current, nil, nil, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return current, nil, nil, ErrNotFound
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p != nil {
		current = recoveryPolicyClone(p.Response)
	}
	if current.Version != req.ExpectedVersion {
		return current, nil, nil, ErrWorkflowRecoveryPolicyConflict
	}
	if p == nil {
		return current, reviews, executions, nil
	}
	seen := map[string]bool{}
	for _, event := range p.History {
		e := event.Entry
		if e.Kind != "candidates_observed" || e.CandidateEvidence == nil || event.RecordedAt.After(c.ObservedAt) || !event.RecordedAt.After(c.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)) {
			continue
		}
		r := recoveryCandidateEvidenceReview(e)
		if r.review.ObservedAt.Before(c.Start) || r.review.ObservedAt.After(c.End) || req.Workflow != "" && r.workflow != req.Workflow {
			continue
		}
		reviews = append(reviews, r)
		seen[r.review.ID] = true
		if len(reviews) > api.WorkflowRecoveryPolicyEvaluationReviewsMax+1 {
			sort.Slice(reviews, func(i, j int) bool { return recoveryEvaluationReviewLess(reviews[i], reviews[j]) })
			reviews = reviews[:api.WorkflowRecoveryPolicyEvaluationReviewsMax+1]
		}
	}
	for _, raw := range p.Reviews {
		if err := ctx.Err(); err != nil {
			return current, nil, nil, err
		}
		var review api.WorkflowRecoveryPolicyReview
		if err := json.Unmarshal(raw, &review); err != nil {
			return current, nil, nil, fmt.Errorf("decode historical recovery review: %w", err)
		}
		if seen[review.ID] {
			continue
		}
		if review.ObservedAt.Before(c.Start) || review.ObservedAt.After(c.End) || !sameMemUUID(review.AppID, app) {
			continue
		}
		job := m.eventRecoveryJobs[canonicalMemUUID(review.JobID)]
		if job == nil || !sameMemUUID(job.Job.AppID, app) {
			continue
		}
		workflow := ""
		if job.Job.Selection.WorkflowRuns != nil {
			workflow = job.Job.Selection.WorkflowRuns.Name
		}
		if req.Workflow != "" && workflow != req.Workflow {
			continue
		}
		reviews = append(reviews, recoveryEvaluationReview{review: review, workflow: workflow})
		if len(reviews) > api.WorkflowRecoveryPolicyEvaluationReviewsMax+1 {
			sort.Slice(reviews, func(i, j int) bool { return recoveryEvaluationReviewLess(reviews[i], reviews[j]) })
			reviews = reviews[:api.WorkflowRecoveryPolicyEvaluationReviewsMax+1]
		}
	}
	for _, event := range p.History {
		if err := ctx.Err(); err != nil {
			return current, nil, nil, err
		}
		e := event.Entry
		if e.Kind == "retry_transition" && e.EvaluationReason == "child_created" && !e.OccurredAt.Before(c.Start.Add(-recoveryEvaluationLookback(current.Policy, req.Policy))) && !e.OccurredAt.After(c.End) && event.RecordedAt.After(c.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)) && !event.RecordedAt.After(c.ObservedAt) {
			executions = append(executions, e)
		}
	}
	return current, reviews, executions, nil
}
