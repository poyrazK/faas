package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) PendingExecutionPipelineCallbacks(ctx context.Context, after string, limit int) ([]ExecutionPipelineDelivery, error) {
	if limit < 1 || limit > api.ExecutionTaskCallbackBatch || after != "" && !validEnvironmentID(after) {
		return nil, ErrExecutionInvalid
	}
	rows, err := sqlc.New().PipelineCallbacksPending(ctx, s.pool, sqlc.PipelineCallbacksPendingParams{AfterID: after, BatchLimit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]ExecutionPipelineDelivery, 0, len(rows))
	for _, r := range rows {
		p, e := pipelineFromSQL(sqlc.ExecutionWorkflowPipeline{JobID: r.JobID, Policy: r.Policy, DeadlineAt: r.DeadlineAt, DueAt: r.DueAt, CancelRequested: r.CancelRequested, Completion: r.Completion, DeliveredAt: r.DeliveredAt, Journal: r.Journal})
		if e != nil {
			return nil, e
		}
		out = append(out, pipelineDelivery(ExecutionWorkflowJob{ID: p.JobID, AccountID: pgUUIDString(r.AccountID)}, p))
	}
	return out, nil
}
func (s *PgStore) MarkExecutionPipelineCallbackDelivered(ctx context.Context, account, id string) error {
	if !validEnvironmentID(account) || !validEnvironmentID(id) {
		return ErrNotFound
	}
	n, err := sqlc.New().PipelineCallbackDelivered(ctx, s.pool, sqlc.PipelineCallbackDeliveredParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)})
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *PgStore) ExecutionPipelineDeliveryByID(ctx context.Context, account, id string) (ExecutionPipelineDelivery, error) {
	if !validEnvironmentID(account) || !validEnvironmentID(id) {
		return ExecutionPipelineDelivery{}, ErrNotFound
	}
	r, err := sqlc.New().PipelineDeliveryGet(ctx, s.pool, sqlc.PipelineDeliveryGetParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(id)})
	if err != nil {
		return ExecutionPipelineDelivery{}, mapErr(err)
	}
	p, err := pipelineFromSQL(sqlc.ExecutionWorkflowPipeline{JobID: r.JobID, Policy: r.Policy, DeadlineAt: r.DeadlineAt, DueAt: r.DueAt, CancelRequested: r.CancelRequested, Completion: r.Completion, DeliveredAt: r.DeliveredAt, Journal: r.Journal})
	return pipelineDelivery(ExecutionWorkflowJob{ID: id, AccountID: account}, p), err
}
func (s *PgStore) ExecutionPipelineDeliveryForRun(ctx context.Context, account, id string) (ExecutionPipelineDelivery, error) {
	if !validEnvironmentID(account) || !validEnvironmentID(id) {
		return ExecutionPipelineDelivery{}, ErrNotFound
	}
	r, err := sqlc.New().PipelineDeliveryForRun(ctx, s.pool, sqlc.PipelineDeliveryForRunParams{AccountID: mustPgUUID(account), ID: mustPgUUID(id)})
	if err != nil {
		return ExecutionPipelineDelivery{}, mapErr(err)
	}
	p, err := pipelineFromSQL(sqlc.ExecutionWorkflowPipeline{JobID: r.JobID, Policy: r.Policy, DeadlineAt: r.DeadlineAt, DueAt: r.DueAt, CancelRequested: r.CancelRequested, Completion: r.Completion, DeliveredAt: r.DeliveredAt, Journal: r.Journal})
	return pipelineDelivery(ExecutionWorkflowJob{ID: p.JobID, AccountID: account}, p), err
}
