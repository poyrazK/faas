package state

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func newRecoveryExecutionHealth() *api.EventRecoveryExecutionHealth {
	return &api.EventRecoveryExecutionHealth{Coverage: "bounded_retained_execution_jobs", CountsComplete: true, JobLimit: api.EventRecoveryExecutionHealthJobsMax, WaitWarningSeconds: int64(api.EventRecoveryExecutionWaitWarning / time.Second), RetentionWarningSeconds: int64(api.EventRecoveryExecutionRetentionWarning / time.Second), Jobs: []api.EventRecoveryExecutionJobHealth{}}
}

func appendRecoveryExecutionHealth(out *api.EventRecoveryExecutionHealth, id, parent, state string, completed, now time.Time, captured bool, queued int64, summary api.EventRecoveryExecutionSummary) {
	job := api.EventRecoveryExecutionJobHealth{JobID: id, ParentJobID: parent, State: state, Status: "waiting", CompletedAt: completed, WaitAgeSeconds: max(0, now.Sub(completed).Seconds()), RetainUntil: completed.Add(api.EventRecoveryJobRetention), NotificationPending: !captured, QueuedCount: queued, Execution: summary}
	job.UntrackedCount = max(0, queued-summary.TrackedCount)
	job.UnknownCount = summary.Unknown + job.UntrackedCount
	job.UnresolvedCount = max(0, queued-summary.SavedResults)
	job.AwaitingSavedResultsCount = max(0, job.UnresolvedCount-summary.Queued-summary.Running-summary.Retrying-job.UnknownCount)
	job.ProlongedWait = now.Sub(completed) >= api.EventRecoveryExecutionWaitWarning
	job.RetentionRisk = !job.RetainUntil.After(now.Add(api.EventRecoveryExecutionRetentionWarning))
	out.ObservedJobs++
	out.WaitingJobs++
	if job.ProlongedWait {
		out.ProlongedWaitJobs++
		job.Status = "prolonged_wait"
	}
	if job.UnknownCount > 0 {
		out.UnknownJobs++
		job.Status = "unknown"
	}
	if job.RetentionRisk {
		out.RetentionRiskJobs++
		job.Status = "retention_risk"
	}
	if len(out.Jobs) < api.EventRecoveryExecutionHealthSampleMax {
		out.Jobs = append(out.Jobs, job)
	}
}

func observeRecoveryExecutionHealth(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, app string, now time.Time) (*api.EventRecoveryExecutionHealth, error) {
	out := newRecoveryExecutionHealth()
	rows, err := q.EventRecoveryExecutionHealthJobs(ctx, db, sqlc.EventRecoveryExecutionHealthJobsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), NowAt: pgtypeFromTime(now), JobLimit: api.EventRecoveryExecutionHealthJobsMax + 1})
	if err != nil {
		return nil, err
	}
	if len(rows) > api.EventRecoveryExecutionHealthJobsMax {
		out.CountsComplete = false
		rows = rows[:api.EventRecoveryExecutionHealthJobsMax]
	}
	for _, row := range rows {
		var selection api.EventRecoveryRequest
		if err := json.Unmarshal(row.Selection, &selection); err != nil {
			return nil, err
		}
		var observations map[int64]api.EventRecoveryExecution
		if selection.Mode == "workflow" {
			observations, err = observeWorkflowRecoveryExecutions(ctx, q, db, account, uuidString(row.ID), 0, api.AutomationRecoveryBatchMaxRuns, now)
		} else {
			observations, err = observeRecoveryExecutions(ctx, q, db, account, uuidString(row.ID), 0, api.EventRecoveryRecipientsMax, now)
		}
		if err != nil {
			return nil, err
		}
		summary := api.EventRecoveryExecutionSummary{ObservedAt: now}
		for _, observation := range observations {
			addRecoveryExecution(&summary, observation)
		}
		appendRecoveryExecutionHealth(out, uuidString(row.ID), selection.ParentJobID, row.State, timeFromPgtype(row.CompletedAt), now, row.ExecutionNotificationCaptured, row.QueuedCount, summary)
		if selection.WorkflowRuns != nil {
			attention, e := workflowRecoveryAttentionRead(ctx, q, db, account, api.EventRecoveryJob{ID: uuidString(row.ID), AppID: app, Selection: selection}, now)
			if e != nil {
				return nil, e
			}
			attachWorkflowRecoveryAttention(out, uuidString(row.ID), attention)
		}

	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (m *MemStore) recoveryExecutionHealthLocked(ctx context.Context, account, app string, now time.Time) (*api.EventRecoveryExecutionHealth, error) {
	out := newRecoveryExecutionHealth()
	entries := []*memEventRecoveryJob{}
	for _, job := range m.eventRecoveryJobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameMemUUID(job.AccountID, account) || !sameMemUUID(job.Job.AppID, app) || (job.Job.Selection.Mode != "execution" && job.Job.Selection.Mode != "workflow") || job.Job.ExecutionFinishedAt != nil || (job.Job.Selection.Mode != "workflow" && (job.Job.CompletedAt == nil || job.Job.CompletedAt.After(now) || job.Job.State != "completed" && job.Job.State != "cancelled")) || job.Job.CreatedAt.After(now) {
			continue
		}
		unresolved := false
		for _, item := range job.Items {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if item.State != "queued" {
				continue
			}
			if job.Job.Selection.Mode == "workflow" {
				observed := m.workflowRecoveryExecutionLocked(job, item, now)
				if observed.Source != "recovery_result" {
					unresolved = true
					break
				}
				continue
			}
			saved, ok := m.eventRecoveryExecutionResults[recoveryResultKey{job.Job.ID, item.Position}]
			if !ok || saved.InvocationID != item.ReplayInvocationID || item.ReplayGeneration == nil || saved.Generation != *item.ReplayGeneration || !saved.CreatedAt.Equal(item.ReplayCreatedAt) || saved.Execution.RecordedAt == nil || saved.Execution.RecordedAt.After(now) {
				unresolved = true
				break
			}
		}
		if !unresolved {
			continue
		}
		entries = append(entries, job)
		sort.Slice(entries, func(i, j int) bool {
			if workflowRecoveryMonitorStart(entries[i].Job).Equal(workflowRecoveryMonitorStart(entries[j].Job)) {
				return entries[i].Job.ID < entries[j].Job.ID
			}
			return workflowRecoveryMonitorStart(entries[i].Job).Before(workflowRecoveryMonitorStart(entries[j].Job))
		})
		if len(entries) > api.EventRecoveryExecutionHealthJobsMax+1 {
			entries = entries[:api.EventRecoveryExecutionHealthJobsMax+1]
		}
	}
	if len(entries) > api.EventRecoveryExecutionHealthJobsMax {
		out.CountsComplete = false
		entries = entries[:api.EventRecoveryExecutionHealthJobsMax]
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		response := m.eventRecoveryObservedResponseLocked(entry, now)
		appendRecoveryExecutionHealth(out, response.ID, response.Selection.ParentJobID, response.State, workflowRecoveryMonitorStart(response), now, entry.ExecutionNotificationCaptured, response.QueuedCount, *response.Execution)
		if entry.Job.Selection.WorkflowRuns != nil {
			attention, e := m.workflowRecoveryAttentionLocked(ctx, entry, now)
			if e != nil {
				return nil, e
			}
			attachWorkflowRecoveryAttention(out, response.ID, attention)
		}

	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func workflowRecoveryMonitorStart(job api.EventRecoveryJob) time.Time {
	if job.CompletedAt != nil {
		return *job.CompletedAt
	}
	return job.CreatedAt
}
func attachWorkflowRecoveryAttention(out *api.EventRecoveryExecutionHealth, id string, attention api.WorkflowRecoveryExecutionAttention) {
	if attention.StalledCount > 0 {
		out.StalledJobs++
	}
	if attention.DeadlineExceededCount > 0 {
		out.DeadlineExceededJobs++
	}
	for i := range out.Jobs {
		if out.Jobs[i].JobID != id {
			continue
		}
		out.Jobs[i].Attention = &attention
		if attention.DeadlineExceededCount > 0 {
			out.Jobs[i].Status = "deadline_exceeded"
		} else if attention.StalledCount > 0 {
			out.Jobs[i].Status = "stalled"
		} else if attention.UnknownCount > 0 {
			out.Jobs[i].Status = "unknown"
		} else if attention.CapacityWaitingCount > 0 {
			out.Jobs[i].Status = "capacity_wait"
		} else if attention.ScheduledWaitingCount > 0 && attention.ScheduledWaitingCount == len(attention.Targets) {
			out.Jobs[i].Status = "scheduled_wait"
		}
	}
}
