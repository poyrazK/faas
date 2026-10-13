package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"reflect"
	"time"
)

type WorkflowRecoveryCancellationStore interface {
	WorkflowRecoveryCancellation(context.Context, string, string, api.WorkflowRecoveryCancellationRequest, bool) (api.WorkflowRecoveryCancellationResponse, error)
}

func recoveryCancellationClassify(job api.EventRecoveryJob, item api.EventRecoveryItem, run *WorkflowRun, execution api.EventRecoveryExecution, operation bool, req api.WorkflowRecoveryCancellationRequest, preview bool) api.WorkflowRecoveryCancellationResponse {
	out := api.WorkflowRecoveryCancellationResponse{JobID: job.ID, Position: req.Position, ResumeCount: *req.ExpectedResumeCount, RunID: item.WorkflowRunID, Preview: preview, Outcome: "not_admitted"}
	if item.Position != req.Position || item.State != "queued" || item.ExpectedResumeCount == nil {
		return out
	}
	if *item.ExpectedResumeCount+1 != out.ResumeCount {
		out.Outcome = "changed"
		return out
	}
	if run == nil {
		out.Outcome = "unknown"
		return out
	}
	out.Status = run.Status
	if run.ResumeCount != out.ResumeCount || !sameMemUUID(run.AppID, job.AppID) || run.WorkflowName != job.Selection.WorkflowRuns.Name {
		out.Outcome = "changed"
		return out
	}
	if operation {
		out.Outcome = "operation_owned"
		return out
	}
	if run.CancelledAt != nil {
		out.Outcome = "already_cancelled"
		return out
	}
	if run.Status == WorkflowRunStatusSucceeded || run.Status == WorkflowRunStatusFailed || run.Status == WorkflowRunStatusDead {
		out.Outcome = "already_terminal"
		return out
	}
	out.Outcome = "unknown"
	if execution.Source != "recovery_result" && (execution.State == "queued" || execution.State == "running") {
		out.Outcome = "eligible"
	}
	return out
}
func (s *PgStore) WorkflowRecoveryCancellation(ctx context.Context, account, id string, req api.WorkflowRecoveryCancellationRequest, preview bool) (api.WorkflowRecoveryCancellationResponse, error) {
	var out api.WorkflowRecoveryCancellationResponse
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin recovery cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.EventRecoveryLock(ctx, tx, sqlc.EventRecoveryLockParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)}); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	job, err := getEventRecoveryMetadata(ctx, q, tx, account, id)
	if err != nil {
		return out, err
	}
	if job.Selection.WorkflowRuns == nil {
		return out, ErrEventRecoveryQuery
	}

	if !preview && req.RequestID != "" {
		receipt, e := pgWorkflowRecoveryActionReceipt(ctx, q, tx, account, id, req.RequestID)
		if e == nil {
			if !reflect.DeepEqual(receipt.Request, req) {
				return out, ErrEventRecoveryRequestConflict
			}
			return receipt.Response, tx.Commit(ctx)
		}
		if !errors.Is(e, ErrNotFound) {
			return out, e
		}
		n, e := q.WorkflowRecoveryActionReceiptCount(ctx, tx, mustPgUUID(id))
		if e != nil {
			return out, e
		}
		if n >= api.WorkflowRecoveryActionReceiptsMax {
			return out, ErrWorkflowRecoveryActionReceiptLimit
		}
	}
	items, err := q.EventRecoveryItems(ctx, tx, sqlc.EventRecoveryItemsParams{JobID: mustPgUUID(id), AfterPosition: req.Position - 1, PageLimit: 1})
	if err != nil {
		return out, err
	}
	item := api.EventRecoveryItem{}
	if len(items) > 0 && items[0].Position == req.Position {
		item = recoveryStoredItem(items[0])
	}
	var run *WorkflowRun
	var observed api.EventRecoveryExecution
	operation := false
	if item.WorkflowRunID != "" {
		row, e := q.LockWorkflowRunForManualRetry(ctx, tx, item.WorkflowRunID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return out, e
		}
		if e == nil {
			run = workflowRunFromSQLC(row)
		}
		_, e = q.GetCustomerOperationForWorkflow(ctx, tx, mustPgUUID(item.WorkflowRunID))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return out, e
		}
		operation = e == nil
		observations, e := observeWorkflowRecoveryExecutions(ctx, q, tx, account, id, req.Position-1, req.Position, time.Now().UTC())
		if e != nil {
			return out, e
		}
		observed = observations[req.Position]
	}
	out = recoveryCancellationClassify(job, item, run, observed, operation, req, preview)
	if !preview && out.Outcome == "eligible" {
		run, err = s.cancelWorkflowRunTx(ctx, tx, item.WorkflowRunID, "cancelled by operator")
		if err != nil {
			return out, err
		}
		actor := recoveryAuditActor(ctx, "workflow_cancelled")
		err = q.WorkflowRecoveryCancellationHistory(ctx, tx, sqlc.WorkflowRecoveryCancellationHistoryParams{JobID: mustPgUUID(id), NowAt: pgtypeFromTime(time.Now().UTC()), ActorKind: actor.Kind, ActorID: actor.ID, Reason: req.Reason, Position: req.Position, RunID: mustPgUUID(run.ID), ResumeCount: int32(run.ResumeCount)})
		if err != nil {
			return out, err
		}
		out.Outcome = "cancelled"
		out.Status = run.Status
	}

	if !preview && req.RequestID != "" {
		actor := recoveryAuditActor(ctx, "workflow_cancelled")
		request, _ := json.Marshal(req)
		response, _ := json.Marshal(out)
		err = q.WorkflowRecoveryActionReceiptInsert(ctx, tx, sqlc.WorkflowRecoveryActionReceiptInsertParams{JobID: mustPgUUID(id), RequestID: mustPgUUID(req.RequestID), Request: request, Response: response, CapturedAt: pgtypeFromTime(time.Now().UTC()), ActorKind: actor.Kind, ActorID: actor.ID})
		if err != nil {
			return out, fmt.Errorf("save recovery action receipt: %w", err)
		}
	}
	return out, tx.Commit(ctx)
}
func (m *MemStore) WorkflowRecoveryCancellation(ctx context.Context, account, id string, req api.WorkflowRecoveryCancellationRequest, preview bool) (api.WorkflowRecoveryCancellationResponse, error) {
	var out api.WorkflowRecoveryCancellationResponse
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	if err := req.Validate(); err != nil {
		return out, fmt.Errorf("%w: %w", ErrEventRecoveryQuery, err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	job := m.eventRecoveryJobs[canonicalMemUUID(id)]
	if job == nil || !sameMemUUID(job.AccountID, account) {
		return out, ErrNotFound
	}
	if job.Job.Selection.WorkflowRuns == nil {
		return out, ErrEventRecoveryQuery
	}

	if !preview && req.RequestID != "" {
		if encoded, ok := job.ActionReceipts[req.RequestID]; ok {
			var receipt api.WorkflowRecoveryActionReceipt
			if err := json.Unmarshal(encoded, &receipt); err != nil {
				return out, err
			}
			if !reflect.DeepEqual(receipt.Request, req) {
				return out, ErrEventRecoveryRequestConflict
			}
			return receipt.Response, nil
		}
		if len(job.ActionReceipts) >= api.WorkflowRecoveryActionReceiptsMax {
			return out, ErrWorkflowRecoveryActionReceiptLimit
		}
	}
	var item memEventRecoveryItem
	for _, candidate := range job.Items {
		if candidate.Position == req.Position {
			item = candidate
			break
		}
	}
	var run *WorkflowRun
	if r, ok := m.workflowRuns[item.WorkflowRunID]; ok {
		run = &r
	}
	_, operation := m.operationForWorkflowLocked(item.WorkflowRunID)
	observed := m.workflowRecoveryExecutionLocked(job, item, time.Now().UTC())
	out = recoveryCancellationClassify(job.Job, item.EventRecoveryItem, run, observed, operation, req, preview)
	if !preview && out.Outcome == "eligible" {
		if _, err := m.cancelWorkflowRunLocked(item.WorkflowRunID, "cancelled by operator"); err != nil {
			return out, err
		}
		auditCtx, _ := WithEventRecoveryReason(ctx, req.Reason)
		memRecoveryHistory(auditCtx, job, "workflow_cancelled", job.Job.State, job.Job.RatePerSecond, time.Now().UTC())
		last := &job.History[len(job.History)-1]
		position, count := req.Position, *req.ExpectedResumeCount
		last.TargetPosition = &position
		last.TargetRunID = item.WorkflowRunID
		last.TargetResumeCount = &count
		out.Outcome = "cancelled"
		out.Status = WorkflowRunStatusFailed
	}

	if !preview && req.RequestID != "" {
		actor := recoveryAuditActor(ctx, "workflow_cancelled")
		receipt := api.WorkflowRecoveryActionReceipt{Request: req, Response: out, CapturedAt: time.Now().UTC(), ActorKind: actor.Kind, ActorID: actor.ID}
		encoded, _ := json.Marshal(receipt)
		if job.ActionReceipts == nil {
			job.ActionReceipts = map[string][]byte{}
		}
		job.ActionReceipts[req.RequestID] = encoded
	}
	return out, nil
}
