package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) PreviewWorkflowRecoveryPolicyChange(ctx context.Context, account, app string, req api.SetWorkflowRecoveryPolicyRequest) (api.WorkflowRecoveryPolicyChangePreview, error) {
	var out api.WorkflowRecoveryPolicyChangePreview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !m.eventRecoveryAppLocked(account, app) {
		return out, ErrNotFound
	}
	now := time.Now().UTC()
	queue, last := m.workflowRecoveryAutomaticQueueLocked(app, now, true)
	if queue.Policy.Version != req.ExpectedVersion {
		return out, ErrWorkflowRecoveryPolicyConflict
	}
	budget := m.recoveryPolicyChangeBudgetLocked(app, now)
	out = recoveryPolicyChangePreview(queue, req.Policy, last, budget)
	jobs := []*memEventRecoveryJob{}
	for _, j := range m.eventRecoveryJobs {
		if sameMemUUID(j.Job.AppID, app) && j.Job.Selection.WorkflowRuns != nil && recoveryPolicyHas(req.Policy.WorkflowNames, j.Job.Selection.WorkflowRuns.Name) {
			jobs = append(jobs, j)
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].Job.CreatedAt.Equal(jobs[j].Job.CreatedAt) {
			return jobs[i].Job.ID > jobs[j].Job.ID
		}
		return jobs[i].Job.CreatedAt.After(jobs[j].Job.CreatedAt)
	})
	if len(jobs) > api.WorkflowRecoveryPolicyJobsPerPoll {
		out.JobsTruncated = true
		jobs = jobs[:api.WorkflowRecoveryPolicyJobsPerPoll]
	}
	for _, job := range jobs {
		candidates, err := m.recoveryPolicyChangeCandidatesLocked(ctx, job, req.Policy, now)
		if err != nil {
			return out, fmt.Errorf("inspect proposed policy targets: %w", err)
		}
		out.InspectedJobs++
		addRecoveryPolicyChangeCandidates(&out, job.Job, candidates, &budget)
	}
	if err := m.previewInitialPolicyChangeLocked(ctx, account, app, &out, &budget, last); err != nil {
		return out, err
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	return out, nil
}
func (m *MemStore) recoveryPolicyChangeBudgetLocked(app string, now time.Time) recoveryPolicyBudget {
	b := recoveryPolicyBudget{Existing: map[string]bool{}}
	for _, r := range m.initialRecoveryReviews {
		if !sameMemUUID(r.AppID, app) {
			continue
		}
		if r.PreparedAt.After(b.Last) {
			b.Last = r.PreparedAt
		}
		if r.PreparedAt.After(now.Add(-time.Hour)) {
			b.Hourly++
		}
	}
	p := m.recoveryPolicies[canonicalMemUUID(app)]
	if p == nil {
		return b
	}
	for _, raw := range p.Reviews {
		var r api.WorkflowRecoveryPolicyReview
		if json.Unmarshal(raw, &r) != nil || r.Plan == nil || m.eventRecoveryJobs[r.JobID] == nil {
			continue
		}
		if r.ObservedAt.After(b.Last) {
			b.Last = r.ObservedAt
		}
		for _, d := range r.Decisions {
			if d.Outcome != "prepared" {
				continue
			}
			b.Existing[recoveryPolicyTargetKey(d.RunID, d.ResumeCount, d.Action)] = true
			if r.ObservedAt.After(now.Add(-time.Hour)) {
				b.Hourly++
			}
		}
	}
	return b
}
func (m *MemStore) recoveryPolicyChangeCandidatesLocked(ctx context.Context, job *memEventRecoveryJob, p api.WorkflowRecoveryPolicy, now time.Time) ([]recoveryPolicyCandidate, error) {
	attention, err := m.workflowRecoveryAttentionLocked(ctx, job, now)
	if err != nil {
		return nil, err
	}
	byPosition := map[int64]string{}
	for _, a := range attention.Targets {
		byPosition[a.Position] = a.Status
	}
	candidates := []recoveryPolicyCandidate{}
	for _, item := range job.Items {
		row := api.EventRecoveryPreflightItem{}
		if workflowRecoveryRetryParentTerminal(job.Job.State) {
			row, err = m.workflowRecoveryPreflightItemLocked(ctx, job, item, now, true)
			if err != nil {
				return nil, err
			}
		}
		var run *WorkflowRun
		if r, ok := m.workflowRuns[item.WorkflowRunID]; ok {
			run = &r
		}
		_, operation := m.operationForWorkflowLocked(item.WorkflowRunID)
		c, ok := recoveryPolicyCandidateForItem(p, job.Job, item.EventRecoveryItem, row, byPosition[item.Position], run, m.workflowRecoveryExecutionLocked(job, item, now), operation)
		if ok {
			candidates = append(candidates, c)
		}
	}
	return candidates, nil
}
