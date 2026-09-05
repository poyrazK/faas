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

func (s *PgStore) ListRuntimeConfigs(ctx context.Context, scope RuntimeConfigScope, scopeID string) ([]RuntimeConfig, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, config_key, scope, scope_id, desired_value,
		       COALESCE(effective_value, 'null'::jsonb), version,
		       rollout_percent, rollout_state,
		       apply_mode, status, COALESCE(last_error, ''),
		       COALESCE(actor_id::text, ''), COALESCE(reason, ''),
		       updated_at, applied_at
		FROM runtime_config_entries
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR scope_id = $2)
		ORDER BY scope, scope_id, config_key`, string(scope), scopeID)
	if err != nil {
		return nil, fmt.Errorf("state: list runtime config: %w", err)
	}
	defer rows.Close()
	var out []RuntimeConfig
	for rows.Next() {
		row, err := scanRuntimeConfig(rows)
		if err != nil {
			return nil, fmt.Errorf("state: scan runtime config: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate runtime config: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetRuntimeConfig(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string) (RuntimeConfig, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, config_key, scope, scope_id, desired_value,
		       COALESCE(effective_value, 'null'::jsonb), version,
		       rollout_percent, rollout_state,
		       apply_mode, status, COALESCE(last_error, ''),
		       COALESCE(actor_id::text, ''), COALESCE(reason, ''),
		       updated_at, applied_at
		FROM runtime_config_entries
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3`,
		key, string(scope), scopeID)
	config, err := scanRuntimeConfig(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfig{}, ErrRuntimeConfigNotFound
	}
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("state: get runtime config: %w", err)
	}
	return config, nil
}

func (s *PgStore) UpsertRuntimeConfig(ctx context.Context, update RuntimeConfigUpdate) (RuntimeConfig, error) {
	if update.Scope == "" {
		update.Scope = RuntimeConfigScopeGlobal
	}
	if update.DesiredValue == nil {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config value is nil")
	}
	if !json.Valid(update.DesiredValue) {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config value is invalid json")
	}
	if update.ApplyMode == "" {
		update.ApplyMode = RuntimeConfigApplyHot
	}
	rolloutPercent := 100
	if update.RolloutPercent != nil {
		rolloutPercent = *update.RolloutPercent
	}
	if rolloutPercent < 0 || rolloutPercent > 100 {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config rollout percent must be between 0 and 100")
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id             string
		currentVersion int64
		oldValue       []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT id, version, desired_value
		FROM runtime_config_entries
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3
		FOR UPDATE`, update.Key, string(update.Scope), update.ScopeID).
		Scan(&id, &currentVersion, &oldValue)
	if errors.Is(err, pgx.ErrNoRows) {
		if update.ExpectedVersion != nil && *update.ExpectedVersion != 0 {
			return RuntimeConfig{}, ErrRuntimeConfigConflict
		}
		id = uuid.NewString()
		currentVersion = 1
		if _, err := tx.Exec(ctx, `
			INSERT INTO runtime_config_entries
			    (id, config_key, scope, scope_id, desired_value,
			     effective_value, version, rollout_percent, rollout_state,
			     apply_mode, status, actor_id, reason)
			VALUES ($1, $2, $3, $4, $5::jsonb, NULL, $6, $7, $8, $9,
			        'pending', NULLIF($10, '')::uuid, $11)`,
			id, update.Key, string(update.Scope), update.ScopeID,
			string(update.DesiredValue), currentVersion, rolloutPercent,
			runtimeConfigRolloutState(rolloutPercent), string(update.ApplyMode),
			update.ActorID, update.Reason); err != nil {
			return RuntimeConfig{}, fmt.Errorf("state: insert runtime config: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO runtime_config_revisions
			    (entry_id, config_key, scope, scope_id, version,
			     rollout_percent, old_value, new_value, actor_id, reason)
			VALUES ($1, $2, $3, $4, $5, $6, NULL, $7::jsonb, NULLIF($8, '')::uuid, $9)`,
			id, update.Key, string(update.Scope), update.ScopeID,
			currentVersion, rolloutPercent, string(update.DesiredValue), update.ActorID, update.Reason); err != nil {
			return RuntimeConfig{}, fmt.Errorf("state: insert runtime config revision: %w", err)
		}
	} else if err != nil {
		return RuntimeConfig{}, fmt.Errorf("state: lock runtime config: %w", err)
	} else {
		if update.ExpectedVersion != nil && *update.ExpectedVersion != currentVersion {
			return RuntimeConfig{}, ErrRuntimeConfigConflict
		}
		currentVersion++
		if _, err := tx.Exec(ctx, `
			UPDATE runtime_config_entries
			SET desired_value = $2::jsonb,
			    effective_value = NULL,
			    version = $3,
			    rollout_percent = $4,
			    rollout_state = $5,
			    apply_mode = $6,
			    status = 'pending',
			    last_error = NULL,
			    actor_id = NULLIF($7, '')::uuid,
			    reason = $8,
			    updated_at = now(),
			    applied_at = NULL
			WHERE id = $1`,
			id, string(update.DesiredValue), currentVersion, rolloutPercent,
			runtimeConfigRolloutState(rolloutPercent), string(update.ApplyMode),
			update.ActorID, update.Reason); err != nil {
			return RuntimeConfig{}, fmt.Errorf("state: update runtime config: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO runtime_config_revisions
			    (entry_id, config_key, scope, scope_id, version,
			     rollout_percent, old_value, new_value, actor_id, reason)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb, $8::jsonb,
			        NULLIF($9, '')::uuid, $10)`,
			id, update.Key, string(update.Scope), update.ScopeID,
			currentVersion, rolloutPercent, string(oldValue), string(update.DesiredValue),
			update.ActorID, update.Reason); err != nil {
			return RuntimeConfig{}, fmt.Errorf("state: insert runtime config revision: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return RuntimeConfig{}, fmt.Errorf("state: commit runtime config: %w", err)
	}
	return s.GetRuntimeConfig(ctx, update.Key, update.Scope, update.ScopeID)
}

func (s *PgStore) MarkRuntimeConfigApplied(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64, effectiveValue json.RawMessage, applyErr string) error {
	if effectiveValue == nil {
		effectiveValue = json.RawMessage("null")
	}
	status := string(RuntimeConfigApplied)
	var appliedAt any = time.Now().UTC()
	if applyErr != "" {
		status = string(RuntimeConfigFailed)
		appliedAt = nil
		if len(applyErr) > 1024 {
			applyErr = applyErr[:1024]
		}
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE runtime_config_entries
		SET effective_value = $5::jsonb,
		    status = $6,
		    last_error = NULLIF($7, ''),
		    applied_at = $8,
		    updated_at = now()
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3
		  AND version = $4`, key, string(scope), scopeID, version,
		string(effectiveValue), status, applyErr, appliedAt)
	if err != nil {
		return fmt.Errorf("state: mark runtime config applied: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRuntimeConfigConflict
	}
	return nil
}

// MarkRuntimeConfigRolloutState updates the operator-facing rollout lifecycle
// without changing the desired/effective value. It is used by the safety
// controller after an automatic rollback so the configuration screen shows
// why the canary stopped while the restored value remains applied.
func (s *PgStore) MarkRuntimeConfigRolloutState(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64, rolloutState RuntimeConfigRolloutState, lastError string) error {
	if !validRuntimeConfigRolloutState(rolloutState) {
		return fmt.Errorf("state: invalid runtime config rollout state %q", rolloutState)
	}
	if len(lastError) > 1024 {
		lastError = lastError[:1024]
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE runtime_config_entries
		SET rollout_state = $5,
		    last_error = NULLIF($6, ''),
		    updated_at = now()
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3 AND version = $4`,
		key, string(scope), scopeID, version, string(rolloutState), lastError)
	if err != nil {
		return fmt.Errorf("state: mark runtime config rollout state: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRuntimeConfigConflict
	}
	return nil
}

func (s *PgStore) ListRuntimeConfigRevisions(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, limit int) ([]RuntimeConfigRevision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, config_key, scope, scope_id, version,
		       rollout_percent, COALESCE(old_value, 'null'::jsonb), new_value,
		       COALESCE(actor_id::text, ''), COALESCE(reason, ''), created_at
		FROM runtime_config_revisions
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3
		ORDER BY version DESC
		LIMIT $4`, key, string(scope), scopeID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list runtime config revisions: %w", err)
	}
	defer rows.Close()
	var out []RuntimeConfigRevision
	for rows.Next() {
		var revision RuntimeConfigRevision
		var scopeText string
		var oldValue, newValue []byte
		if err := rows.Scan(&revision.ID, &revision.Key, &scopeText, &revision.ScopeID,
			&revision.Version, &revision.RolloutPercent, &oldValue, &newValue, &revision.ActorID,
			&revision.Reason, &revision.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan runtime config revision: %w", err)
		}
		revision.Scope = RuntimeConfigScope(scopeText)
		revision.OldValue = json.RawMessage(oldValue)
		revision.NewValue = json.RawMessage(newValue)
		out = append(out, revision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate runtime config revisions: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetRuntimeConfigRevision(ctx context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64) (RuntimeConfigRevision, error) {
	var revision RuntimeConfigRevision
	var scopeText string
	var oldValue, newValue []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id, config_key, scope, scope_id, version,
		       rollout_percent, COALESCE(old_value, 'null'::jsonb), new_value,
		       COALESCE(actor_id::text, ''), COALESCE(reason, ''), created_at
		FROM runtime_config_revisions
		WHERE config_key = $1 AND scope = $2 AND scope_id = $3 AND version = $4`,
		key, string(scope), scopeID, version).
		Scan(&revision.ID, &revision.Key, &scopeText, &revision.ScopeID,
			&revision.Version, &revision.RolloutPercent, &oldValue, &newValue, &revision.ActorID,
			&revision.Reason, &revision.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return RuntimeConfigRevision{}, ErrRuntimeConfigNotFound
	}
	if err != nil {
		return RuntimeConfigRevision{}, fmt.Errorf("state: get runtime config revision: %w", err)
	}
	revision.Scope = RuntimeConfigScope(scopeText)
	revision.OldValue = json.RawMessage(oldValue)
	revision.NewValue = json.RawMessage(newValue)
	return revision, nil
}

type runtimeConfigScanner interface {
	Scan(dest ...any) error
}

func scanRuntimeConfig(row runtimeConfigScanner) (RuntimeConfig, error) {
	var (
		config                            RuntimeConfig
		scope, rolloutState, mode, status string
		desired, effective                []byte
		actorID, reason, lastError        string
	)
	err := row.Scan(
		&config.ID, &config.Key, &scope, &config.ScopeID,
		&desired, &effective, &config.Version, &config.RolloutPercent, &rolloutState, &mode, &status,
		&lastError, &actorID, &reason, &config.UpdatedAt, &config.AppliedAt,
	)
	if err != nil {
		return RuntimeConfig{}, err
	}
	config.Scope = RuntimeConfigScope(scope)
	config.RolloutState = RuntimeConfigRolloutState(rolloutState)
	config.ApplyMode = RuntimeConfigApplyMode(mode)
	config.Status = RuntimeConfigStatus(status)
	config.DesiredValue = json.RawMessage(desired)
	config.EffectiveValue = json.RawMessage(effective)
	config.LastError = lastError
	config.ActorID = actorID
	config.Reason = reason
	return config, nil
}
