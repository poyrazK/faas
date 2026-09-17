package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const managedRealtimeDrainOperationColumns = `
	id, account_id, app_id, endpoint_id, status, reason, dry_run,
	matched, closed, gone, failed, result, created_at, completed_at`

func (s *PgStore) CreateManagedRealtimeDrainOperation(ctx context.Context, input ManagedRealtimeDrainOperationInput) (ManagedRealtimeDrainOperation, error) {
	id := uuid.NewString()
	row := s.pool.QueryRow(ctx, `
		INSERT INTO managed_realtime_drain_operations
			(id, account_id, app_id, endpoint_id, status, reason, dry_run, matched)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7)
		RETURNING `+managedRealtimeDrainOperationColumns,
		id, input.AccountID, input.AppID, input.EndpointID, input.Reason, input.DryRun, input.Matched)
	return scanManagedRealtimeDrainOperation(row)
}

func (s *PgStore) CompleteManagedRealtimeDrainOperation(ctx context.Context, id, accountID, endpointID string, status ManagedRealtimeDrainOperationStatus, result json.RawMessage, matched, closed, gone, failed int) (ManagedRealtimeDrainOperation, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE managed_realtime_drain_operations
		   SET status = $4, matched = $5, closed = $6, gone = $7, failed = $8,
		       result = $9::jsonb, completed_at = now()
		 WHERE id = $1 AND account_id = $2 AND endpoint_id = $3
		RETURNING `+managedRealtimeDrainOperationColumns,
		id, accountID, endpointID, string(status), matched, closed, gone, failed, string(result))
	return scanManagedRealtimeDrainOperation(row)
}

func (s *PgStore) GetManagedRealtimeDrainOperation(ctx context.Context, accountID, endpointID, id string) (ManagedRealtimeDrainOperation, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT `+managedRealtimeDrainOperationColumns+`
		  FROM managed_realtime_drain_operations
		 WHERE id = $1 AND account_id = $2 AND endpoint_id = $3`, id, accountID, endpointID)
	return scanManagedRealtimeDrainOperation(row)
}

type managedRealtimeDrainOperationScanner interface {
	Scan(dest ...any) error
}

func scanManagedRealtimeDrainOperation(row managedRealtimeDrainOperationScanner) (ManagedRealtimeDrainOperation, error) {
	var (
		op     ManagedRealtimeDrainOperation
		status string
		result []byte
	)
	err := row.Scan(&op.ID, &op.AccountID, &op.AppID, &op.EndpointID, &status, &op.Reason,
		&op.DryRun, &op.Matched, &op.Closed, &op.Gone, &op.Failed, &result, &op.CreatedAt, &op.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeDrainOperation{}, ErrManagedRealtimeDrainOperationNotFound
	}
	if err != nil {
		return ManagedRealtimeDrainOperation{}, fmt.Errorf("state: scan managed realtime drain operation: %w", err)
	}
	op.Status = ManagedRealtimeDrainOperationStatus(status)
	op.Result = append(json.RawMessage(nil), result...)
	return op, nil
}
