package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// WorkflowFailureEligibilityStore observes retained state without preparing retries.
type WorkflowFailureEligibilityStore interface {
	ObserveNextWorkflowFailureEligibility(context.Context, time.Time) (bool, error)
}

func failureEligibilityAssessment(r recoveryEvaluationReview, d api.WorkflowRecoveryPolicyDecision, current, proposed api.WorkflowRecoveryPolicy, now time.Time) *api.WorkflowRecoveryFailureEligibilityAssessment {
	if r.eligibility == nil || r.eligibility.ObservedAt.After(now) {
		return nil
	}
	observation := recoveryPolicyClone(*r.eligibility)
	out := &api.WorkflowRecoveryFailureEligibilityAssessment{Observation: observation, Freshness: "fresh"}
	if !observation.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureObservationFreshness)) {
		out.Freshness = "stale"
	}
	assess := func(p api.WorkflowRecoveryPolicy) api.WorkflowRecoveryPolicyAssessment {
		check := d
		check.Outcome, check.PreflightOutcome = "prepared", "eligible"
		observed := r
		observed.source = "eligibility_observer"
		a := recoveryEvaluationEligibility(p, observed, check)
		if a.Eligibility == "qualifies" {
			a.Eligibility, a.Preparation = "unknown", "unknown"
			switch {
			case out.Freshness == "stale":
				a.Reason = "later_eligibility_stale"
			case observation.Reason == "inspection_error":
				a.Reason = "later_eligibility_inspection_error"
			case observation.Reason == "eligible" || observation.Reason == "capacity":
				a.Eligibility, a.Reason = "qualifies", "eligible_at_later_observation"
			default:
				a.Eligibility, a.Reason = "excluded", "later_preflight_"+observation.Reason
			}
		}
		return a
	}
	out.Current, out.Proposed = assess(current), assess(proposed)
	return out
}

func failureEligibilityDiagnostic(e workflowFailureEvidence, d api.WorkflowRunDiagnosticsResponse, err error, owned bool, now time.Time) (*api.WorkflowRecoveryFailureEligibility, error) {
	count := e.ResumeCount
	row := api.EventRecoveryPreflightItem{WorkflowRunID: e.RunID, ExpectedResumeCount: &count}
	if err := workflowPreflightDiagnostics(&row, e.Workflow, d, err, owned); err != nil {
		return nil, err
	}
	if row.Reason != "run_not_retained" && row.Reason != "changed" && d.Status != e.FailureStatus {
		row.Reason = "changed"
	}
	return &api.WorkflowRecoveryFailureEligibility{ObservedAt: now.UTC().Truncate(time.Microsecond), Reason: row.Reason, Blockers: append([]string{}, row.Blockers...)}, nil
}

func (m *MemStore) ObserveNextWorkflowFailureEligibility(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	type candidate struct {
		app   string
		index int
		fact  workflowFailureEvidence
	}
	rows := []candidate{}
	for app, facts := range m.workflowFailureEvidence {
		if _, ok := m.eventSubscriptionAppLocked(app); !ok {
			continue
		}
		for i, e := range facts {
			if e.ObservedAt.After(now) || !e.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureRetention)) || e.Eligibility != nil && e.Eligibility.ObservedAt.After(now.Add(-api.WorkflowRecoveryFailureObserverRefresh)) {
				continue
			}
			rows = append(rows, candidate{app, i, e})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i].fact, rows[j].fact
		var at, bt time.Time
		if a.Eligibility != nil {
			at = a.Eligibility.ObservedAt
		}
		if b.Eligibility != nil {
			bt = b.Eligibility.ObservedAt
		}
		if !at.Equal(bt) {
			return at.Before(bt)
		}
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.Before(b.ObservedAt)
		}
		return a.ID < b.ID
	})
	if len(rows) == 0 {
		return false, nil
	}
	candidateRow := rows[0]
	e := candidateRow.fact
	app, _ := m.eventSubscriptionAppLocked(e.AppID)
	d, err := m.workflowRunDiagnosticsLocked(ctx, WorkflowDiagnosticsOptions{RunID: e.RunID, AppID: e.AppID, AccountID: app.AccountID})
	_, owned := m.operationForWorkflowLocked(e.RunID)
	observation, err := failureEligibilityDiagnostic(e, d, err, owned, now)
	if err != nil {
		observation = &api.WorkflowRecoveryFailureEligibility{ObservedAt: now.UTC().Truncate(time.Microsecond), Reason: "inspection_error", Blockers: []string{}}
	}
	var parent *memEventRecoveryJob
	var position int64
	for _, job := range m.eventRecoveryJobs {
		if !sameMemUUID(job.AccountID, app.AccountID) || !sameMemUUID(job.Job.AppID, e.AppID) || job.Job.Selection.Mode != "workflow" || job.Job.Selection.WorkflowRuns == nil || job.Job.Selection.WorkflowRuns.Name != e.Workflow || !workflowRecoveryRetryParentTerminal(job.Job.State) || job.Job.CreatedAt.After(now) {
			continue
		}
		for _, item := range job.Items {
			saved, ok := job.WorkflowResults[item.Position]
			if !ok || item.State != "queued" || !sameMemUUID(item.WorkflowRunID, e.RunID) || item.ExpectedResumeCount == nil || *item.ExpectedResumeCount+1 != e.ResumeCount || saved.RunID != e.RunID || saved.ResumeNumber != e.ResumeCount || saved.Execution.State != "failed" || saved.Execution.RecordedAt == nil || saved.Execution.RecordedAt.After(now) {
				continue
			}
			if parent == nil || job.Job.CreatedAt.After(parent.Job.CreatedAt) || job.Job.CreatedAt.Equal(parent.Job.CreatedAt) && (job.Job.ID > parent.Job.ID || job.Job.ID == parent.Job.ID && item.Position < position) {
				parent, position = job, item.Position
			}
		}
	}
	if parent != nil {
		observation.ParentJobID, observation.ParentPosition = parent.Job.ID, position
	} else if observation.Reason == "eligible" || observation.Reason == "capacity" {
		observation.Reason = "parent_not_retained"
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.workflowFailureEvidence[candidateRow.app][candidateRow.index].Eligibility = observation
	return true, nil
}
