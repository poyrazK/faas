package state

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type memWorkflowRecoveryResult struct {
	RunID        string
	ResumeNumber int
	Execution    api.EventRecoveryExecution
}

// Mutation-time capture only. First confirmed terminal evidence wins.
func (m *MemStore) captureWorkflowRecoveryResultLocked(run WorkflowRun, now time.Time) {
	app, owned := m.eventSubscriptionAppLocked(run.AppID)
	if !owned {
		return
	}
	for _, job := range m.eventRecoveryJobs {
		if job.Job.Selection.Mode != "workflow" || !sameMemUUID(job.AccountID, app.AccountID) || !sameMemUUID(job.Job.AppID, run.AppID) || job.Job.Selection.WorkflowRuns.Name != run.WorkflowName {
			continue
		}
		for _, item := range job.Items {
			if item.State != "queued" || item.WorkflowRunID != run.ID || item.ExpectedResumeCount == nil {
				continue
			}
			if _, saved := job.WorkflowResults[item.Position]; saved {
				continue
			}
			var admitted *time.Time
			for _, resume := range m.workflowResumes[run.ID] {
				if resume.ResumeNumber == *item.ExpectedResumeCount+1 && sameMemUUID(resume.AccountID, job.AccountID) && !resume.CreatedAt.Before(job.Job.CreatedAt) {
					t := resume.CreatedAt
					admitted = &t
					break
				}
			}
			cancelled := run.CancelledAt != nil || run.LastError != nil && *run.LastError == "cancelled by operator"
			execution := workflowRecoveryExecution(now, run.Status, run.ResumeCount, *item.ExpectedResumeCount, admitted, run.UpdatedAt, run.FinishedAt, cancelled)
			switch execution.State {
			case "succeeded", "failed", "cancelled", "superseded":
			default:
				continue
			}
			execution.Source, execution.EvidenceSource = "recovery_result", "workflow_run"
			execution.RecordedAt = cloneEventReceiptTime(&now)
			if job.WorkflowResults == nil {
				job.WorkflowResults = map[int64]memWorkflowRecoveryResult{}
			}
			job.WorkflowResults[item.Position] = memWorkflowRecoveryResult{run.ID, *item.ExpectedResumeCount + 1, execution}
		}
	}
}
