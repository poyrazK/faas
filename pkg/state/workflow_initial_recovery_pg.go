package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) initialFailureReadTx(ctx context.Context, account, app string) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin initial failure inspection: %w", err)
	}
	_, err = sqlc.New().WorkflowRecoveryAutomaticQueueApp(ctx, tx, sqlc.WorkflowRecoveryAutomaticQueueAppParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app)})
	if err != nil {
		_ = tx.Rollback(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scope initial failure inspection: %w", err)
	}
	return tx, nil
}
func (s *PgStore) ListWorkflowInitialFailures(ctx context.Context, account, app string, o api.WorkflowInitialFailureOptions) (api.WorkflowInitialFailures, error) {
	var out api.WorkflowInitialFailures
	now := time.Now().UTC()
	c, limit, err := initialFailureWindow(account, app, o, now)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := sqlc.New().WorkflowInitialFailures(ctx, tx, sqlc.WorkflowInitialFailuresParams{AppID: mustPgUUID(app), Workflow: o.Workflow, RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureRetention)), ObservedAt: pgtypeFromTime(c.ObservedAt), AfterID: c.ID, AfterAt: pgtypeFromTime(c.At), PageLimit: int32(limit + 1)})
	if err != nil {
		return out, fmt.Errorf("list initial failures: %w", err)
	}
	items := []api.WorkflowInitialFailure{}
	for _, r := range rows {
		items = append(items, initialFailureAPI(workflowFailureEvidence{ID: uuidString(r.ID), AppID: uuidString(r.AppID), RunID: uuidString(r.RunID), Workflow: r.Workflow, FailureStatus: r.FailureStatus, ObservedAt: timeFromPgtype(r.ObservedAt), CompletedAt: timestamptzToTimePtr(r.CompletedAt)}))
	}
	out = initialFailurePage(c, limit, items)
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("finish initial failure inventory: %w", err)
	}
	return out, nil
}
func (s *PgStore) PreviewWorkflowInitialRecovery(ctx context.Context, account, app, id string) (api.WorkflowInitialRecoveryPreview, error) {
	var out api.WorkflowInitialRecoveryPreview
	if err := eventRecoveryIDs(account, app); err != nil {
		return out, err
	}
	if err := eventRecoveryIDs(account, id); err != nil {
		return out, err
	}
	now := time.Now().UTC()
	ctx, cancel := context.WithTimeout(ctx, api.EventRecoveryRequestTimeout)
	defer cancel()
	tx, err := s.initialFailureReadTx(ctx, account, app)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err = previewWorkflowInitialRecoveryTx(ctx, sqlc.New(), tx, account, app, id, now)
	if err != nil {
		return out, err
	}
	if err = tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("finish initial recovery preview: %w", err)
	}
	return out, nil
}

func previewWorkflowInitialRecoveryTx(ctx context.Context, q *sqlc.Queries, tx sqlc.DBTX, account, app, id string, now time.Time) (api.WorkflowInitialRecoveryPreview, error) {
	var out api.WorkflowInitialRecoveryPreview
	r, err := q.WorkflowInitialFailure(ctx, tx, sqlc.WorkflowInitialFailureParams{ID: mustPgUUID(id), AppID: mustPgUUID(app), RetainedAfter: pgtypeFromTime(now.Add(-api.WorkflowRecoveryFailureRetention)), ObservedAt: pgtypeFromTime(now)})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, fmt.Errorf("inspect initial failure evidence: %w", err)
	}
	e := workflowFailureEvidence{ID: uuidString(r.ID), AppID: uuidString(r.AppID), RunID: uuidString(r.RunID), Workflow: r.Workflow, FailureStatus: r.FailureStatus, ObservedAt: timeFromPgtype(r.ObservedAt), CompletedAt: timestamptzToTimePtr(r.CompletedAt)}
	d, diagnosticErr := workflowRunDiagnosticsRead(ctx, q, tx, WorkflowDiagnosticsOptions{RunID: e.RunID, AppID: app, AccountID: account})
	_, ownerErr := q.GetCustomerOperationForWorkflow(ctx, tx, r.RunID)
	if ownerErr != nil && !errors.Is(ownerErr, pgx.ErrNoRows) {
		return out, fmt.Errorf("inspect initial failure owner: %w", ownerErr)
	}
	observation, err := failureEligibilityDiagnostic(e, d, diagnosticErr, ownerErr == nil, now)
	if err != nil {
		return out, err
	}
	return initialRecoveryPreview(e, observation), nil
}
