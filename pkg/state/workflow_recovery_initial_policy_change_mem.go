package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) previewInitialPolicyChangeLocked(ctx context.Context, account, app string, out *api.WorkflowRecoveryPolicyChangePreview, budget *recoveryPolicyBudget, last *time.Time) error {
	if !initialPolicyChangeEnabled(out.Proposed) {
		return nil
	}
	pressure := initialPolicyChangePressure{unresolved: m.initialAutomaticUnresolvedLocked(app, out.ObservedAt), hourly: out.HourlyExecutions, last: last}
	for _, j := range m.eventRecoveryJobs {
		if sameMemUUID(j.AccountID, account) && eventRecoveryActive(j.Job.State) {
			pressure.active++
		}
	}
	for _, r := range m.initialRecoveryReviews {
		if sameMemUUID(r.AppID, app) && (r.PreparedAt.After(out.ObservedAt.Add(-api.WorkflowRecoveryHistoryRetention)) || initialReviewUnresolved(r)) {
			pressure.retained++
		}
	}
	out.InitialFailures = newInitialPolicyChangePreview(pressure)
	failures := []api.WorkflowInitialFailure{}
	for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app)] {
		if e.ResumeCount == 0 && !e.ObservedAt.After(out.ObservedAt) && e.ObservedAt.After(out.ObservedAt.Add(-api.WorkflowRecoveryFailureRetention)) {
			failures = append(failures, initialFailureAPI(e))
		}
	}
	sort.Slice(failures, func(i, j int) bool {
		a, b := initialPolicyChangeMatches(out.Proposed, failures[i]), initialPolicyChangeMatches(out.Proposed, failures[j])
		if a != b {
			return a
		}
		return failures[i].ID < failures[j].ID
	})
	if len(failures) > api.WorkflowRecoveryPolicyJobsPerPoll {
		out.InitialFailures.Truncated = true
		failures = failures[:api.WorkflowRecoveryPolicyJobsPerPoll]
	}
	for _, f := range failures {
		if err := ctx.Err(); err != nil {
			return err
		}
		preview := api.WorkflowInitialRecoveryPreview{Failure: f, ObservedAt: out.ObservedAt, Blockers: []string{}}
		pending := false
		if out.Proposed.Enabled && initialPolicyChangeMatches(out.Proposed, f) {
			var err error
			preview, err = m.previewWorkflowInitialRecoveryLocked(ctx, account, app, f.ID, out.ObservedAt)
			if err != nil {
				return err
			}
			pending = m.initialRecoveryPendingLocked(app, f.RunID)
		}
		addInitialPolicyChangeCandidate(out, preview, pending, budget, &pressure)
	}
	return nil
}
