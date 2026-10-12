package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type workflowFailureEvidence struct {
	ID            string
	AppID         string
	RunID         string
	Workflow      string
	ResumeCount   int
	FailureStatus string
	ObservedAt    time.Time
	CompletedAt   *time.Time
	Eligibility   *api.WorkflowRecoveryFailureEligibility
}
type recoveryFailureSnapshot struct {
	StartedAt time.Time
	Failures  []workflowFailureEvidence
}

func (m *MemStore) captureWorkflowFailureLocked(run WorkflowRun, now time.Time) {
	if run.Status != WorkflowRunStatusFailed && run.Status != WorkflowRunStatusDead || run.CancelledAt != nil || run.LastError != nil && *run.LastError == "cancelled by operator" {
		return
	}
	if _, ok := m.eventSubscriptionAppLocked(run.AppID); !ok {
		return
	}
	if run.FinishedAt != nil && run.FinishedAt.After(now) {
		return
	}
	app := canonicalMemUUID(run.AppID)
	for _, e := range m.workflowFailureEvidence[app] {
		if sameMemUUID(e.RunID, run.ID) && e.ResumeCount == run.ResumeCount {
			return
		}
	}
	if m.workflowFailureEvidence == nil {
		m.workflowFailureEvidence = map[string][]workflowFailureEvidence{}
	}
	cutoff := now.Add(-api.WorkflowRecoveryFailureRetention)
	previous := m.workflowFailureEvidence[app]
	retained := previous[:0]
	for _, e := range previous {
		if e.ObservedAt.After(cutoff) {
			retained = append(retained, e)
		}
	}
	clear(previous[len(retained):])
	e := workflowFailureEvidence{ID: canonicalMemUUID(newID()), AppID: app, RunID: canonicalMemUUID(run.ID), Workflow: run.WorkflowName, ResumeCount: run.ResumeCount, FailureStatus: run.Status, ObservedAt: now.UTC().Truncate(time.Microsecond), CompletedAt: cloneEventReceiptTime(run.FinishedAt)}
	if e.CompletedAt != nil {
		completed := e.CompletedAt.UTC().Truncate(time.Microsecond)
		e.CompletedAt = &completed
	}
	retained = append(retained, e)
	if len(retained) > api.WorkflowRecoveryFailureEvidenceMax {
		retained = append([]workflowFailureEvidence(nil), retained[len(retained)-api.WorkflowRecoveryFailureEvidenceMax:]...)
	}
	m.workflowFailureEvidence[app] = retained
}
func (m *MemStore) recoveryFailureSnapshotLocked(ctx context.Context, app string, req api.WorkflowRecoveryPolicyEvaluationRequest, c recoveryHistoryCursor) (recoveryFailureSnapshot, error) {
	out := recoveryFailureSnapshot{StartedAt: m.workflowFailureCaptureStartedAt, Failures: []workflowFailureEvidence{}}
	for _, e := range m.workflowFailureEvidence[canonicalMemUUID(app)] {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if e.ObservedAt.Before(c.Start) || e.ObservedAt.After(c.End) || !e.ObservedAt.After(c.ObservedAt.Add(-api.WorkflowRecoveryFailureRetention)) || e.ObservedAt.After(c.ObservedAt) || req.Workflow != "" && e.Workflow != req.Workflow {
			continue
		}
		e.CompletedAt = cloneEventReceiptTime(e.CompletedAt)
		e.Eligibility = recoveryPolicyClone(e.Eligibility)
		out.Failures = append(out.Failures, e)
	}
	sort.Slice(out.Failures, func(i, j int) bool {
		a, b := out.Failures[i], out.Failures[j]
		if !a.ObservedAt.Equal(b.ObservedAt) {
			return a.ObservedAt.Before(b.ObservedAt)
		}
		return a.ID < b.ID
	})
	if len(out.Failures) > api.WorkflowRecoveryPolicyEvaluationFailuresMax+1 {
		out.Failures = out.Failures[:api.WorkflowRecoveryPolicyEvaluationFailuresMax+1]
	}
	return out, nil
}

func recoveryEvaluationWithFailures(reviews []recoveryEvaluationReview, snapshot recoveryFailureSnapshot) ([]recoveryEvaluationReview, api.WorkflowRecoveryFailureCoverage) {
	coverage := api.WorkflowRecoveryFailureCoverage{CaptureStartedAt: snapshot.StartedAt, Coverage: "partial"}
	failures := snapshot.Failures
	if len(failures) > api.WorkflowRecoveryPolicyEvaluationFailuresMax {
		coverage.Truncated = true
		failures = failures[:api.WorkflowRecoveryPolicyEvaluationFailuresMax]
	}
	coverage.CapturedFailures = len(failures)
	reviewed := map[string]bool{}
	for _, r := range reviews {
		for _, d := range r.review.Decisions {
			if d.Trigger == "saved_failure" && d.Action == "retry" {
				key := recoveryPolicyTargetKey(canonicalMemUUID(d.RunID), d.ResumeCount, d.Action)
				captured := d.PreflightOutcome != ""
				switch d.Outcome {
				case "prepared", "already_prepared", "generation_limit", "cooldown", "hourly_budget":
					captured = true
				}
				reviewed[key] = reviewed[key] || captured
			}
		}
	}
	for _, e := range failures {
		if proof, seen := reviewed[recoveryPolicyTargetKey(e.RunID, e.ResumeCount, "retry")]; seen {
			coverage.ReviewedFailures++
			if !proof {
				coverage.MissingEligibilityEvidence++
			}
			continue
		}
		coverage.UnreviewedFailures++
		coverage.MissingEligibilityEvidence++
		reviews = append(reviews, recoveryEvaluationReview{source: "failure_inventory", eligibility: e.Eligibility, evidenceID: "failure:" + e.ID, workflow: e.Workflow, review: api.WorkflowRecoveryPolicyReview{AppID: e.AppID, ObservedAt: e.ObservedAt, Decisions: []api.WorkflowRecoveryPolicyDecision{{RunID: e.RunID, ResumeCount: e.ResumeCount, Trigger: "saved_failure", Action: "retry", Outcome: "captured_failure", FailureStatus: e.FailureStatus}}}})
	}
	return reviews, coverage
}
