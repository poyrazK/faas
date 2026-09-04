package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PgStore) CreateRuntimeConfigOperation(ctx context.Context, config RuntimeConfig, actorID, reason string) (RuntimeConfigOperation, error) {
	if config.ApplyMode != RuntimeConfigApplyGraceful && config.ApplyMode != RuntimeConfigApplyRolling && config.ApplyMode != RuntimeConfigApplyBreakGlass {
		return RuntimeConfigOperation{}, fmt.Errorf("state: runtime config operation requires non-hot apply mode")
	}
	if !json.Valid(config.DesiredValue) {
		return RuntimeConfigOperation{}, fmt.Errorf("state: runtime config operation value is invalid json")
	}
	id := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO runtime_config_operations
		    (id, config_key, scope, scope_id, config_version, desired_value,
		     apply_mode, actor_id, reason)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, NULLIF($8, '')::uuid, $9)`,
		id, config.Key, string(config.Scope), config.ScopeID, config.Version,
		string(config.DesiredValue), string(config.ApplyMode), actorID, reason)
	if err != nil {
		return RuntimeConfigOperation{}, fmt.Errorf("state: create runtime config operation: %w", err)
	}
	return s.GetRuntimeConfigOperation(ctx, id)
}

func (s *PgStore) GetRuntimeConfigOperation(ctx context.Context, id string) (RuntimeConfigOperation, error) {
	row := s.pool.QueryRow(ctx, runtimeConfigOperationSelect+` WHERE id = $1`, id)
	operation, err := scanRuntimeConfigOperation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigOperation{}, ErrRuntimeConfigNotFound
	}
	if err != nil {
		return RuntimeConfigOperation{}, fmt.Errorf("state: get runtime config operation: %w", err)
	}
	return operation, nil
}

func (s *PgStore) ClaimPendingRuntimeConfigOperation(ctx context.Context) (RuntimeConfigOperation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeConfigOperation{}, fmt.Errorf("state: claim runtime config operation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	row := tx.QueryRow(ctx, `
		WITH next_operation AS (
			SELECT id AS operation_id
			FROM runtime_config_operations
			WHERE status = 'pending'
			ORDER BY requested_at ASC
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		UPDATE runtime_config_operations AS op
		SET status = 'running', phase = 'claimed', started_at = now()
		FROM next_operation
		WHERE op.id = next_operation.operation_id
		RETURNING `+runtimeConfigOperationColumns)
	operation, err := scanRuntimeConfigOperation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigOperation{}, ErrRuntimeConfigNotFound
	}
	if err != nil {
		return RuntimeConfigOperation{}, fmt.Errorf("state: claim runtime config operation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return RuntimeConfigOperation{}, fmt.Errorf("state: claim runtime config operation commit: %w", err)
	}
	return operation, nil
}

func (s *PgStore) MarkRuntimeConfigOperationSucceeded(ctx context.Context, id string, effectiveValue json.RawMessage, appliedCount, targetCount int) error {
	if effectiveValue == nil {
		effectiveValue = json.RawMessage("null")
	}
	return s.finishRuntimeConfigOperation(ctx, id, RuntimeConfigOperationSucceeded, "complete", "", effectiveValue, appliedCount, targetCount, false)
}

func (s *PgStore) MarkRuntimeConfigOperationFailed(ctx context.Context, id, phase, errMsg string) error {
	if len(errMsg) > 1024 {
		errMsg = errMsg[:1024]
	}
	return s.finishRuntimeConfigOperation(ctx, id, RuntimeConfigOperationFailed, phase, errMsg, nil, 0, 0, false)
}

func (s *PgStore) MarkRuntimeConfigOperationBlocked(ctx context.Context, id, phase, reason string) error {
	if len(reason) > 1024 {
		reason = reason[:1024]
	}
	return s.finishRuntimeConfigOperation(ctx, id, RuntimeConfigOperationBlocked, phase, reason, nil, 0, 0, true)
}

func (s *PgStore) finishRuntimeConfigOperation(ctx context.Context, id string, status RuntimeConfigOperationStatus, phase, message string, effectiveValue json.RawMessage, appliedCount, targetCount int, allowPending bool) error {
	if effectiveValue == nil {
		effectiveValue = json.RawMessage("null")
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("state: finish runtime config operation begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	statusPredicate := "status = 'running'"
	if allowPending {
		statusPredicate = "status IN ('pending', 'running')"
	}
	var key, scope, scopeID string
	var version int64
	if err := tx.QueryRow(ctx, `
		UPDATE runtime_config_operations
		SET status = $2, phase = $3, error = NULLIF($4, ''),
		    effective_value = CASE WHEN $2 = 'succeeded' THEN $5::jsonb ELSE effective_value END,
		    applied_count = CASE WHEN $2 = 'succeeded' THEN $6 ELSE applied_count END,
		    target_count = CASE WHEN $2 = 'succeeded' THEN $7 ELSE target_count END,
		    failed_count = CASE WHEN $2 = 'succeeded' THEN 0 ELSE failed_count END,
		    finished_at = now()
		WHERE id = $1 AND `+statusPredicate+`
		RETURNING config_key, scope, scope_id, config_version`,
		id, string(status), phase, message, string(effectiveValue), appliedCount, targetCount).
		Scan(&key, &scope, &scopeID, &version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrRuntimeConfigNotFound
		}
		return fmt.Errorf("state: finish runtime config operation: %w", err)
	}

	if status == RuntimeConfigOperationSucceeded {
		tag, err := tx.Exec(ctx, `
			UPDATE runtime_config_entries
			SET effective_value = $5::jsonb, status = 'applied', last_error = NULL,
			    applied_at = now(), updated_at = now()
			WHERE config_key = $1 AND scope = $2 AND scope_id = $3 AND version = $4`,
			key, scope, scopeID, version, string(effectiveValue))
		if err != nil {
			return fmt.Errorf("state: apply runtime config operation value: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrRuntimeConfigConflict
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE runtime_config_entries
			SET status = $5, last_error = NULLIF($6, ''), updated_at = now()
			WHERE config_key = $1 AND scope = $2 AND scope_id = $3 AND version = $4`,
			key, scope, scopeID, version, string(status), message); err != nil {
			return fmt.Errorf("state: mark runtime config operation state: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: finish runtime config operation commit: %w", err)
	}
	return nil
}

const runtimeConfigOperationColumns = `
		id, config_key, scope, scope_id, config_version,
		desired_value, COALESCE(effective_value, 'null'::jsonb),
		apply_mode, status, phase, COALESCE(error, ''),
		COALESCE(actor_id::text, ''), COALESCE(reason, ''),
		target_count, applied_count, failed_count,
		requested_at, started_at, finished_at`

const runtimeConfigOperationSelect = `SELECT ` + runtimeConfigOperationColumns + ` FROM runtime_config_operations`

func scanRuntimeConfigOperation(row runtimeConfigScanner) (RuntimeConfigOperation, error) {
	var (
		operation                         RuntimeConfigOperation
		scope, mode, status               string
		desired, effective                []byte
		errorText, actorID, reason, phase string
	)
	err := row.Scan(
		&operation.ID, &operation.Key, &scope, &operation.ScopeID, &operation.Version,
		&desired, &effective, &mode, &status, &phase, &errorText,
		&actorID, &reason, &operation.TargetCount, &operation.AppliedCount,
		&operation.FailedCount, &operation.RequestedAt, &operation.StartedAt,
		&operation.FinishedAt,
	)
	if err != nil {
		return RuntimeConfigOperation{}, err
	}
	operation.Scope = RuntimeConfigScope(scope)
	operation.DesiredValue = json.RawMessage(desired)
	operation.EffectiveValue = json.RawMessage(effective)
	operation.ApplyMode = RuntimeConfigApplyMode(mode)
	operation.Status = RuntimeConfigOperationStatus(status)
	operation.Phase = phase
	operation.Error = errorText
	operation.ActorID = actorID
	operation.Reason = reason
	return operation, nil
}
