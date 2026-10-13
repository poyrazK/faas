package state

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type WorkflowRecoveryExecutionAttentionStore interface {
	ProcessNextWorkflowRecoveryExecutionAttention(context.Context, time.Time) (bool, error)
}

func newWorkflowRecoveryAttention(plan *api.WorkflowRunRecoveryPlan, now time.Time) api.WorkflowRecoveryExecutionAttention {
	stall := plan.ExecutionStallSeconds
	if stall == 0 {
		stall = api.WorkflowRecoveryExecutionStallDefaultSeconds
	}
	return api.WorkflowRecoveryExecutionAttention{ObservedAt: now, DeadlineSeconds: plan.ExecutionDeadlineSeconds, StallSeconds: stall, Targets: []api.WorkflowRecoveryExecutionAttentionTarget{}}
}
func addWorkflowRecoveryAttention(out *api.WorkflowRecoveryExecutionAttention, position int64, run string, count int, execution api.EventRecoveryExecution, admitted *time.Time, progress time.Time, wait string) {
	if execution.Source == "recovery_result" {
		return
	}
	item := api.WorkflowRecoveryExecutionAttentionTarget{Position: position, RunID: run, ResumeCount: count, Status: "unknown", WaitReason: wait, RunURL: "/v1/workflows/runs/" + run, DiagnosticsURL: "/v1/workflows/runs/" + run + "/diagnostics"}
	if (execution.State == "queued" || execution.State == "running") && admitted != nil && !admitted.After(out.ObservedAt) && !progress.Before(*admitted) && !progress.After(out.ObservedAt) {
		item.AdmittedAt = cloneEventReceiptTime(admitted)
		item.LastProgressAt = cloneEventReceiptTime(&progress)
		item.Status = execution.State
		if out.DeadlineSeconds > 0 {
			at := admitted.Add(time.Duration(out.DeadlineSeconds) * time.Second)
			item.DeadlineAt = &at
		}
		switch wait {
		case api.AutomationQueueAppCapacity, api.AutomationQueueTenantCapacity, api.AutomationQueueWorkflowCapacity:
			item.Status = "capacity_wait"
			out.CapacityWaitingCount++
		case api.AutomationQueueScheduled, api.AutomationQueueParkedWait, api.AutomationQueueRetryBackoff:
			item.Status = "scheduled_wait"
			out.ScheduledWaitingCount++
		}
		if item.DeadlineAt != nil && !item.DeadlineAt.After(out.ObservedAt) {
			item.Status = "deadline_exceeded"
			out.DeadlineExceededCount++
		} else if wait == "" || wait == api.AutomationQueueReady {
			if out.ObservedAt.Sub(progress) >= time.Duration(out.StallSeconds)*time.Second {
				item.Status = "stalled"
				out.StalledCount++
			}
		}
	} else {
		out.UnknownCount++
	}
	out.Targets = append(out.Targets, item)
	out.NeedsAttention = out.StalledCount+out.DeadlineExceededCount > 0
}
func workflowRecoveryAttentionRead(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account string, job api.EventRecoveryJob, now time.Time) (api.WorkflowRecoveryExecutionAttention, error) {
	out := newWorkflowRecoveryAttention(job.Selection.WorkflowRuns, now)
	rows, err := q.WorkflowRecoveryExecutionObservations(ctx, db, sqlc.WorkflowRecoveryExecutionObservationsParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(job.ID), ThroughPosition: api.AutomationRecoveryBatchMaxRuns, NowAt: pgtypeFromTime(now)})
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		if r.ResultState != "" && r.ResultRecordedAt.Valid {
			continue
		}
		// Find the immutable run identity from the frozen item, not a newer resume.
		items, err := q.EventRecoveryItems(ctx, db, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(job.ID), AfterPosition: r.Position - 1, PageLimit: 1})
		if err != nil {
			return out, err
		}
		if len(items) != 1 {
			continue
		}
		item := recoveryStoredItem(items[0])
		wait := ""
		observed := workflowRecoveryExecution(now, r.RunStatus, int(r.ResumeCount), int(r.ExpectedResumeCount), timestamptzToTimePtr(r.AdmittedAt), timeFromPgtype(r.RunUpdatedAt), timestamptzToTimePtr(r.FinishedAt), r.Cancelled)
		if observed.State == "queued" {
			d, e := workflowRunDiagnosticsRead(ctx, q, db, WorkflowDiagnosticsOptions{RunID: item.WorkflowRunID, AccountID: account, AppID: job.AppID})
			if e != nil && !errors.Is(e, ErrWorkflowRunNotFound) {
				return out, e
			}
			if e == nil {
				wait = workflowRecoveryAttentionWait(d, now)
			}
		}
		addWorkflowRecoveryAttention(&out, r.Position, item.WorkflowRunID, int(r.ExpectedResumeCount)+1, observed, timestamptzToTimePtr(r.AdmittedAt), timeFromPgtype(r.RunUpdatedAt), wait)
	}
	evidence, err := q.EventRecoveryNotificationEvidence(ctx, db, sqlc.EventRecoveryNotificationEvidenceParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(job.ID)})
	if err != nil {
		return out, err
	}
	var receipts map[string]json.RawMessage
	if err = json.Unmarshal(evidence.NotificationReceipts, &receipts); err != nil {
		return out, err
	}
	_, out.NotificationCaptured = receipts[string(AppWebhookEventRecoveryExecutionAttention)]
	return out, nil
}
func (m *MemStore) workflowRecoveryAttentionLocked(ctx context.Context, job *memEventRecoveryJob, now time.Time) (api.WorkflowRecoveryExecutionAttention, error) {
	out := newWorkflowRecoveryAttention(job.Job.Selection.WorkflowRuns, now)
	_, out.NotificationCaptured = job.NotificationReceipts[string(AppWebhookEventRecoveryExecutionAttention)]
	for _, item := range job.Items {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if item.State != "queued" || item.ExpectedResumeCount == nil {
			continue
		}
		observed := m.workflowRecoveryExecutionLocked(job, item, now)
		run := m.workflowRuns[item.WorkflowRunID]
		var admitted *time.Time
		for _, resume := range m.workflowResumes[item.WorkflowRunID] {
			if resume.ResumeNumber == *item.ExpectedResumeCount+1 && sameMemUUID(resume.AccountID, job.AccountID) && !resume.CreatedAt.Before(job.Job.CreatedAt) {
				at := resume.CreatedAt
				admitted = &at
				break
			}
		}
		wait := ""
		if observed.State == "queued" {
			d, e := m.workflowRunDiagnosticsLocked(ctx, WorkflowDiagnosticsOptions{RunID: item.WorkflowRunID, AccountID: job.AccountID, AppID: job.Job.AppID})
			if e != nil && !errors.Is(e, ErrWorkflowRunNotFound) {
				return out, e
			}
			if e == nil {
				wait = workflowRecoveryAttentionWait(d, now)
			}
		}
		addWorkflowRecoveryAttention(&out, item.Position, item.WorkflowRunID, *item.ExpectedResumeCount+1, observed, admitted, run.UpdatedAt, wait)
	}
	return out, nil
}
func (s *PgStore) ProcessNextWorkflowRecoveryExecutionAttention(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	candidate, err := q.WorkflowRecoveryClaimExecutionAttention(ctx, tx, pgtypeFromTime(now))
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	account := uuidString(candidate.AccountID)
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, uuidString(candidate.ID))
	if err != nil {
		return false, err
	}
	report, err := workflowRecoveryAttentionRead(ctx, q, tx, account, job, now)
	if err != nil {
		return false, err
	}
	if report.NeedsAttention {
		report.NotificationCaptured = true
		payload := api.WorkflowRecoveryExecutionAttentionWebhookPayload{EventID: recoveryNotificationEventID(job.ID, string(AppWebhookEventRecoveryExecutionAttention)), JobID: job.ID, AppID: job.AppID, ObservedAt: now, Attention: report, ItemsURL: "/v1/event-recoveries/" + job.ID + "/items"}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return false, err
		}
		if err = q.WorkflowRecoveryEnqueueExecutionAttention(ctx, tx, sqlc.WorkflowRecoveryEnqueueExecutionAttentionParams{JobID: candidate.ID, EventID: mustPgUUID(payload.EventID), NowAt: pgtypeFromTime(now), Payload: encoded}); err != nil {
			return false, err
		}
	} else {
		if err = q.WorkflowRecoveryDeferExecutionAttention(ctx, tx, sqlc.WorkflowRecoveryDeferExecutionAttentionParams{JobID: candidate.ID, NextAt: pgtypeFromTime(now.Add(api.WorkflowRecoveryExecutionAttentionPollInterval))}); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}
func (m *MemStore) ProcessNextWorkflowRecoveryExecutionAttention(ctx context.Context, now time.Time) (bool, error) {
	if now.IsZero() {
		return false, ErrEventRecoveryQuery
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	jobs := []*memEventRecoveryJob{}
	for _, job := range m.eventRecoveryJobs {
		if job.Job.Selection.WorkflowRuns == nil || job.Job.ExecutionFinishedAt != nil || job.NextExecutionAttentionAt.After(now) {
			continue
		}
		if _, captured := job.NotificationReceipts[string(AppWebhookEventRecoveryExecutionAttention)]; captured {
			continue
		}
		for _, item := range job.Items {
			if item.State == "queued" {
				jobs = append(jobs, job)
				break
			}
		}
	}
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].NextExecutionAttentionAt.Equal(jobs[j].NextExecutionAttentionAt) {
			return jobs[i].Job.ID < jobs[j].Job.ID
		}
		return jobs[i].NextExecutionAttentionAt.Before(jobs[j].NextExecutionAttentionAt)
	})
	if len(jobs) == 0 {
		return false, nil
	}
	job := jobs[0]
	report, err := m.workflowRecoveryAttentionLocked(ctx, job, now)
	if err != nil {
		return false, err
	}
	if !report.NeedsAttention {
		job.NextExecutionAttentionAt = now.Add(api.WorkflowRecoveryExecutionAttentionPollInterval)
		return true, nil
	}
	report.NotificationCaptured = true
	event := AppWebhookEventRecoveryExecutionAttention
	id := recoveryNotificationEventID(job.Job.ID, string(event))
	payload := api.WorkflowRecoveryExecutionAttentionWebhookPayload{EventID: id, JobID: job.Job.ID, AppID: job.Job.AppID, ObservedAt: now, Attention: report, ItemsURL: "/v1/event-recoveries/" + job.Job.ID + "/items"}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	recipients := []string{}
	for _, hook := range m.appWebhooks {
		if hook.Scope == AppWebhookScopeApp && sameMemUUID(hook.AccountID, job.AccountID) && sameMemUUID(hook.AppID, job.Job.AppID) && hook.Enabled && appWebhookMatches(hook.EventFilter, event) {
			recipients = append(recipients, hook.ID)
		}
	}
	sort.Strings(recipients)
	if job.NotificationReceipts == nil {
		job.NotificationReceipts = map[string]recoveryNotificationReceipt{}
	}
	job.NotificationReceipts[string(event)] = recoveryNotificationReceipt{EventID: id, CapturedAt: now, RecipientWebhookIDs: recipients}
	if len(recipients) > 0 {
		if m.appWebhookEventOutbox == nil {
			m.appWebhookEventOutbox = map[string]appWebhookOutboxEvent{}
		}
		m.appWebhookEventOutbox[id] = appWebhookOutboxEvent{ID: id, AccountID: job.AccountID, AppID: job.Job.AppID, Event: event, SourceID: job.Job.ID, Payload: encoded, RecipientWebhookIDs: recipients, CreatedAt: now}
	}
	return true, nil
}

func workflowRecoveryAttentionWait(d api.WorkflowRunDiagnosticsResponse, now time.Time) string {
	reason := d.StateReason
	if d.NextWakeAt != nil {
		at, err := time.Parse(time.RFC3339Nano, *d.NextWakeAt)
		if err == nil && !at.After(now) && (reason == api.AutomationQueueScheduled || reason == api.AutomationQueueParkedWait || reason == api.AutomationQueueRetryBackoff) {
			return api.AutomationQueueReady
		}
	}
	return reason
}
