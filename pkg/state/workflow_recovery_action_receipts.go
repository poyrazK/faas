package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var ErrWorkflowRecoveryActionReceiptLimit = errors.New("recovery action receipt limit reached")

type WorkflowRecoveryActionReceiptStore interface {
	GetWorkflowRecoveryActionReceipt(context.Context, string, string, string) (api.WorkflowRecoveryActionReceipt, error)
	GetEventRecoveryByRequest(context.Context, string, string, string) (api.EventRecoveryJob, error)
}

func pgWorkflowRecoveryActionReceipt(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, account, job, request string) (api.WorkflowRecoveryActionReceipt, error) {
	var out api.WorkflowRecoveryActionReceipt
	row, err := q.WorkflowRecoveryActionReceiptGet(ctx, db, sqlc.WorkflowRecoveryActionReceiptGetParams{AccountID: mustPgUUID(account), JobID: mustPgUUID(job), RequestID: mustPgUUID(request)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("read recovery action receipt: %w", err)
	}
	if err = json.Unmarshal(row.Request, &out.Request); err != nil {
		return out, err
	}
	if err = json.Unmarshal(row.Response, &out.Response); err != nil {
		return out, err
	}
	out.CapturedAt = row.CapturedAt.Time
	out.ActorKind = row.ActorKind
	out.ActorID = row.ActorID
	return out, nil
}
func (s *PgStore) GetWorkflowRecoveryActionReceipt(ctx context.Context, account, job, request string) (api.WorkflowRecoveryActionReceipt, error) {
	if err := eventRecoveryIDs(account, job); err != nil {
		return api.WorkflowRecoveryActionReceipt{}, err
	}
	if err := eventRecoveryIDs(account, request); err != nil {
		return api.WorkflowRecoveryActionReceipt{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	return pgWorkflowRecoveryActionReceipt(ctx, sqlc.New(), s.pool, account, job, request)
}
func (m *MemStore) GetWorkflowRecoveryActionReceipt(ctx context.Context, account, job, request string) (api.WorkflowRecoveryActionReceipt, error) {
	var out api.WorkflowRecoveryActionReceipt
	if err := eventRecoveryIDs(account, job); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, request); err != nil {
		return out, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return out, err
	}
	j := m.eventRecoveryJobs[canonicalMemUUID(job)]
	if j == nil || !sameMemUUID(j.AccountID, account) {
		return out, ErrNotFound
	}
	encoded, ok := j.ActionReceipts[canonicalMemUUID(request)]
	if !ok {
		return out, ErrNotFound
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		return out, err
	}
	return out, nil
}
func (s *PgStore) GetEventRecoveryByRequest(ctx context.Context, account, app, request string) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.EventRecoveryJob{}, err
	}
	if err := eventRecoveryIDs(account, request); err != nil {
		return api.EventRecoveryJob{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	q := sqlc.New()
	id, err := q.EventRecoveryByRequest(ctx, s.pool, sqlc.EventRecoveryByRequestParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), RequestID: mustPgUUID(request)})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventRecoveryJob{}, ErrNotFound
	}
	if err != nil {
		return api.EventRecoveryJob{}, fmt.Errorf("find recovery request: %w", err)
	}
	return s.GetEventRecovery(ctx, account, uuidString(id))
}
func (m *MemStore) GetEventRecoveryByRequest(ctx context.Context, account, app, request string) (api.EventRecoveryJob, error) {
	if err := eventRecoveryIDs(account, app); err != nil {
		return api.EventRecoveryJob{}, err
	}
	if err := eventRecoveryIDs(account, request); err != nil {
		return api.EventRecoveryJob{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return api.EventRecoveryJob{}, err
	}
	for _, j := range m.eventRecoveryJobs {
		if sameMemUUID(j.AccountID, account) && sameMemUUID(j.Job.AppID, app) && j.Job.Selection.RequestID == canonicalMemUUID(request) {
			return memEventRecoveryResponse(j), nil
		}
	}
	return api.EventRecoveryJob{}, ErrNotFound
}
