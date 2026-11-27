package state

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ExecutionPipelineJob(ctx context.Context, account, id string, principal *string) (ExecutionWorkflowJob, error) {
	acct, e := parsePgUUID(account)
	if e != nil {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	key, e := parsePgUUID(id)
	if e != nil {
		return ExecutionWorkflowJob{}, ErrNotFound
	}
	var family pgtype.UUID
	if principal != nil {
		family, e = parsePgUUID(*principal)
		if e != nil {
			return ExecutionWorkflowJob{}, ErrNotFound
		}
	}
	j, e := sqlc.New().PipelineScopedJob(ctx, s.pool, sqlc.PipelineScopedJobParams{AccountID: acct, ID: key, PrincipalID: family})
	if e != nil {
		return ExecutionWorkflowJob{}, mapErr(e)
	}
	return pipelineJob(j), nil
}
func (s *PgStore) ListExecutionPipelineEvents(ctx context.Context, account, id string, principal *string, after int64, limit int) (api.ExecutionPipelineEventsResponse, error) {
	j, e := s.ExecutionPipelineJob(ctx, account, id, principal)
	if e != nil {
		return api.ExecutionPipelineEventsResponse{}, e
	}
	p, e := s.ExecutionPipelineByID(ctx, id)
	if e != nil {
		return api.ExecutionPipelineEventsResponse{}, e
	}
	return pipelineEvents(j, p, after, limit)
}
