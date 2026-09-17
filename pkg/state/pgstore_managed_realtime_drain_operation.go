package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const managedRealtimeDrainOperationColumns = `
	id, account_id, app_id, endpoint_id, status, reason, dry_run,
	matched, closed, gone, failed, result, connection_ids, drain_limit,
	truncated, partial, nodes_queried, nodes_unavailable, attempts,
	next_attempt_at, claimed_at, claim_token, last_error, created_at, completed_at`

func (s *PgStore) CreateManagedRealtimeDrainOperation(ctx context.Context, input ManagedRealtimeDrainOperationInput) (ManagedRealtimeDrainOperation, error) {
	id := uuid.NewString()
	row := s.pool.QueryRow(ctx, `
		INSERT INTO managed_realtime_drain_operations
			(id, account_id, app_id, endpoint_id, status, reason, dry_run, matched,
			 connection_ids, drain_limit, truncated, partial, nodes_queried, nodes_unavailable)
		VALUES ($1, $2, $3, $4, 'running', $5, $6, $7, $8::jsonb, $9, $10, $11, $12, $13)
		RETURNING `+managedRealtimeDrainOperationColumns,
		id, input.AccountID, input.AppID, input.EndpointID, input.Reason, input.DryRun, input.Matched,
		managedRealtimeDrainConnectionIDsJSON(input.ConnectionIDs), input.Limit, input.Truncated,
		input.Partial, input.NodesQueried, input.NodesUnavailable)
	return scanManagedRealtimeDrainOperation(row)
}

func (s *PgStore) CompleteManagedRealtimeDrainOperation(ctx context.Context, id, accountID, endpointID string, status ManagedRealtimeDrainOperationStatus, result json.RawMessage, matched, closed, gone, failed int) (ManagedRealtimeDrainOperation, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE managed_realtime_drain_operations
		   SET status = $4, matched = $5, closed = $6, gone = $7, failed = $8,
		       result = $9::jsonb, connection_ids = '[]'::jsonb,
		       claimed_at = NULL, claim_token = NULL, last_error = '', completed_at = now()
		 WHERE id = $1 AND account_id = $2 AND endpoint_id = $3
		RETURNING `+managedRealtimeDrainOperationColumns,
		id, accountID, endpointID, string(status), matched, closed, gone, failed, string(result))
	return scanManagedRealtimeDrainOperation(row)
}

func (s *PgStore) ClaimManagedRealtimeDrainOperations(ctx context.Context, limit int, lease time.Duration) ([]ManagedRealtimeDrainOperationClaim, error) {
	if limit <= 0 {
		return nil, nil
	}
	leaseSeconds := int64(lease.Seconds())
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	rows, err := s.pool.Query(ctx, `
		WITH claimable AS (
			SELECT id
			  FROM managed_realtime_drain_operations
			 WHERE status = 'running'
			   AND next_attempt_at <= now()
			   AND (claimed_at IS NULL OR claimed_at < now() - make_interval(secs => $2))
			 ORDER BY next_attempt_at, created_at, id
			 FOR UPDATE SKIP LOCKED
			 LIMIT $1
		)
		UPDATE managed_realtime_drain_operations op
		   SET claimed_at = now(), claim_token = gen_random_uuid(), attempts = op.attempts + 1
		  FROM claimable
		 WHERE op.id = claimable.id
		RETURNING `+managedRealtimeDrainOperationColumns, limit, leaseSeconds)
	if err != nil {
		return nil, fmt.Errorf("state: claim managed realtime drain operations: %w", err)
	}
	defer rows.Close()
	claims := make([]ManagedRealtimeDrainOperationClaim, 0, limit)
	for rows.Next() {
		op, err := scanManagedRealtimeDrainOperation(rows)
		if err != nil {
			return nil, err
		}
		claims = append(claims, ManagedRealtimeDrainOperationClaim{Operation: op, ClaimToken: op.ClaimToken})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate managed realtime drain operation claims: %w", err)
	}
	return claims, nil
}

func (s *PgStore) RetryManagedRealtimeDrainOperation(ctx context.Context, id, claimToken string, connectionIDs []string, result json.RawMessage, closed, gone, failed int, nextAttemptAt time.Time, lastError string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE managed_realtime_drain_operations
		   SET connection_ids = $3::jsonb, result = $4::jsonb, closed = $5, gone = $6,
		       failed = $7, next_attempt_at = $8, claimed_at = NULL, claim_token = NULL,
		       last_error = left($9, 4096)
		 WHERE id = $1 AND claim_token = $2::uuid AND status = 'running'`,
		id, claimToken, managedRealtimeDrainConnectionIDsJSON(connectionIDs), string(result), closed, gone, failed, nextAttemptAt.UTC(), lastError)
	if err != nil {
		return fmt.Errorf("state: retry managed realtime drain operation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrManagedRealtimeDrainOperationNotFound
	}
	return nil
}

func (s *PgStore) FinishManagedRealtimeDrainOperation(ctx context.Context, id, claimToken string, status ManagedRealtimeDrainOperationStatus, result json.RawMessage, closed, gone, failed int) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE managed_realtime_drain_operations
		   SET status = $3, connection_ids = '[]'::jsonb, result = $4::jsonb,
		       closed = $5, gone = $6, failed = $7, claimed_at = NULL,
		       claim_token = NULL, last_error = '', completed_at = now()
		 WHERE id = $1 AND claim_token = $2::uuid AND status = 'running'`,
		id, claimToken, string(status), string(result), closed, gone, failed)
	if err != nil {
		return fmt.Errorf("state: finish managed realtime drain operation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrManagedRealtimeDrainOperationNotFound
	}
	return nil
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
	op, _, err := scanManagedRealtimeDrainOperationWithClaim(row)
	return op, err
}

func scanManagedRealtimeDrainOperationWithClaim(row managedRealtimeDrainOperationScanner) (ManagedRealtimeDrainOperation, string, error) {
	var (
		op                    ManagedRealtimeDrainOperation
		status                string
		claimToken            string
		claimTokenValue       *string
		result, connectionIDs []byte
	)
	err := row.Scan(&op.ID, &op.AccountID, &op.AppID, &op.EndpointID, &status, &op.Reason,
		&op.DryRun, &op.Matched, &op.Closed, &op.Gone, &op.Failed, &result, &connectionIDs,
		&op.Limit, &op.Truncated, &op.Partial, &op.NodesQueried, &op.NodesUnavailable,
		&op.Attempts, &op.NextAttemptAt, &op.ClaimedAt, &claimTokenValue, &op.LastError, &op.CreatedAt, &op.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManagedRealtimeDrainOperation{}, "", ErrManagedRealtimeDrainOperationNotFound
	}
	if err != nil {
		return ManagedRealtimeDrainOperation{}, "", fmt.Errorf("state: scan managed realtime drain operation: %w", err)
	}
	op.Status = ManagedRealtimeDrainOperationStatus(status)
	op.Result = append(json.RawMessage(nil), result...)
	if claimTokenValue != nil {
		claimToken = *claimTokenValue
		op.ClaimToken = claimToken
	}
	if len(connectionIDs) > 0 {
		if err := json.Unmarshal(connectionIDs, &op.ConnectionIDs); err != nil {
			return ManagedRealtimeDrainOperation{}, "", fmt.Errorf("state: decode managed realtime drain connection ids: %w", err)
		}
	}
	return op, claimToken, nil
}

func managedRealtimeDrainConnectionIDsJSON(values []string) string {
	if values == nil {
		values = []string{}
	}
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
