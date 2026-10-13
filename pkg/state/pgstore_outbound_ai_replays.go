package state

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIReplayStore = (*PgStore)(nil)

// CreateOutboundAIReplay records a pending replay of a captured call
// (ADR-997). The source integration's row lock serializes the daily count.
// A target must be one of the account's customer AI integrations, and one
// whose provider differs from the source's must name a model.
func (s *PgStore) CreateOutboundAIReplay(ctx context.Context, replay OutboundAIReplay) (OutboundAIReplay, error) {
	if len(replay.Targets) == 0 || len(replay.Targets) > api.OutboundAIReplayTargetsMax {
		return OutboundAIReplay{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sourceProvider string
	err = tx.QueryRow(ctx, `
		SELECT ai_provider FROM outbound_integrations
		 WHERE account_id = $1 AND id = $2 AND owner_kind = 'customer' AND provider_auth_mode = 'managed'
		   AND ai_provider IS NOT NULL
		 FOR UPDATE`, mustPgUUID(replay.AccountID), mustPgUUID(replay.IntegrationID)).Scan(&sourceProvider)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIReplay{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	var captured, replayable bool
	err = tx.QueryRow(ctx, `
		SELECT content.log_id IS NOT NULL,
		       content.log_id IS NOT NULL AND NOT content.request_truncated AND content.request_path IS NOT NULL
		  FROM outbound_ai_request_log log
		  LEFT JOIN outbound_ai_request_content content ON content.log_id = log.id
		 WHERE log.id = $1 AND log.integration_id = $2`, mustPgUUID(replay.LogID), mustPgUUID(replay.IntegrationID)).Scan(&captured, &replayable)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIReplay{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	if !captured || !replayable {
		return OutboundAIReplay{}, ErrOutboundAIReplayUnavailable
	}
	if err := s.checkOutboundAIReplayTargets(ctx, tx, replay, sourceProvider); err != nil {
		return OutboundAIReplay{}, err
	}
	var today int64
	if err := tx.QueryRow(ctx, `
		SELECT count(*) FROM outbound_ai_replays
		 WHERE integration_id = $1 AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'`,
		mustPgUUID(replay.IntegrationID)).Scan(&today); err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	if today >= api.OutboundAIReplaysPerDay {
		return OutboundAIReplay{}, ErrOutboundAIReplayLimit
	}
	targetIDs := make([]pgtype.UUID, 0, len(replay.Targets))
	models := make([]string, 0, len(replay.Targets))
	for _, target := range replay.Targets {
		targetIDs = append(targetIDs, mustPgUUID(target.IntegrationID))
		models = append(models, target.Model)
	}
	replay.ID, replay.State = uuid.NewString(), api.OutboundAIReplayPending
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_replays (id, account_id, integration_id, log_id, target_integration_ids, target_models)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at`, mustPgUUID(replay.ID), mustPgUUID(replay.AccountID), mustPgUUID(replay.IntegrationID),
		mustPgUUID(replay.LogID), targetIDs, models).Scan(&replay.CreatedAt); err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	replay.CreatedAt = replay.CreatedAt.UTC()
	return replay, nil
}

func (s *PgStore) checkOutboundAIReplayTargets(ctx context.Context, tx pgx.Tx, replay OutboundAIReplay, sourceProvider string) error {
	for _, target := range replay.Targets {
		var provider string
		err := tx.QueryRow(ctx, `
			SELECT ai_provider FROM outbound_integrations
			 WHERE account_id = $1 AND id = $2 AND owner_kind = 'customer' AND provider_auth_mode = 'managed'
			   AND enabled AND ai_provider IS NOT NULL`,
			mustPgUUID(replay.AccountID), mustPgUUID(target.IntegrationID)).Scan(&provider)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidArgument
		}
		if err != nil {
			return mapErr(err)
		}
		if provider != sourceProvider && target.Model == "" {
			return ErrInvalidArgument
		}
	}
	return nil
}

// GetOutboundAIReplay reads one of the account's replays with the logged
// call it resends and its results.
func (s *PgStore) GetOutboundAIReplay(ctx context.Context, accountID, replayID string) (OutboundAIReplay, error) {
	if _, err := uuid.Parse(replayID); err != nil {
		return OutboundAIReplay{}, ErrNotFound
	}
	replay := OutboundAIReplay{ID: replayID, AccountID: accountID}
	var integrationID, logID pgtype.UUID
	var targets []pgtype.UUID
	var models []string
	var errorCode, sourceText pgtype.Text
	var finishedAt pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		SELECT integration_id, log_id, target_integration_ids, target_models, state, error_code, source_output_text,
		       created_at, finished_at
		  FROM outbound_ai_replays WHERE id = $1 AND account_id = $2`, mustPgUUID(replayID), mustPgUUID(accountID)).
		Scan(&integrationID, &logID, &targets, &models, &replay.State, &errorCode, &sourceText, &replay.CreatedAt, &finishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIReplay{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIReplay{}, mapErr(err)
	}
	replay.IntegrationID, replay.LogID = pgUUIDString(integrationID), pgUUIDString(logID)
	replay.ErrorCode, replay.SourceOutputText, replay.CreatedAt = errorCode.String, sourceText.String, replay.CreatedAt.UTC()
	if finishedAt.Valid {
		finished := finishedAt.Time.UTC()
		replay.FinishedAt = &finished
	}
	for index, target := range targets {
		replay.Targets = append(replay.Targets, OutboundAIReplayTarget{IntegrationID: pgUUIDString(target), Model: models[index]})
	}
	if replay.Source, err = s.outboundAIRequestLogEntry(ctx, logID); err != nil {
		return OutboundAIReplay{}, err
	}
	replay.Results, err = s.outboundAIReplayResults(ctx, replayID)
	return replay, err
}

func (s *PgStore) outboundAIRequestLogEntry(ctx context.Context, logID pgtype.UUID) (OutboundAIRequestLogEntry, error) {
	var entry OutboundAIRequestLogEntry
	var status int16
	var duration int32
	var ttfb pgtype.Int4
	err := s.pool.QueryRow(ctx, `
		SELECT model, status, input_tokens, output_tokens, cost_millicents, duration_ms, ttfb_ms, created_at
		  FROM outbound_ai_request_log WHERE id = $1`, logID).
		Scan(&entry.Model, &status, &entry.InputTokens, &entry.OutputTokens, &entry.CostMillicents, &duration, &ttfb, &entry.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIRequestLogEntry{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIRequestLogEntry{}, mapErr(err)
	}
	entry.ID, entry.Status, entry.DurationMS, entry.CreatedAt = pgUUIDString(logID), int(status), int64(duration), entry.CreatedAt.UTC()
	if ttfb.Valid {
		value := int64(ttfb.Int32)
		entry.TTFBMS = &value
	}
	return entry, nil
}

func (s *PgStore) outboundAIReplayResults(ctx context.Context, replayID string) ([]OutboundAIReplayResult, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT target_integration_id, COALESCE(model, ''), status, COALESCE(outcome, ''), COALESCE(error_code, ''),
		       input_tokens, output_tokens, cost_millicents, duration_ms, ttfb_ms, response_body, response_truncated,
		       COALESCE(response_content_type, ''), output_text
		  FROM outbound_ai_replay_results WHERE replay_id = $1 ORDER BY position`, mustPgUUID(replayID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	results := make([]OutboundAIReplayResult, 0)
	for rows.Next() {
		var result OutboundAIReplayResult
		var target pgtype.UUID
		var status pgtype.Int2
		var duration int32
		var ttfb pgtype.Int4
		if err := rows.Scan(&target, &result.Model, &status, &result.Outcome, &result.ErrorCode, &result.InputTokens,
			&result.OutputTokens, &result.CostMillicents, &duration, &ttfb, &result.ResponseBody, &result.ResponseTruncated,
			&result.ResponseContentType, &result.OutputText); err != nil {
			return nil, mapErr(err)
		}
		result.IntegrationID, result.DurationMS = pgUUIDString(target), int64(duration)
		if status.Valid {
			value := int(status.Int16)
			result.Status = &value
		}
		if ttfb.Valid {
			value := int64(ttfb.Int32)
			result.TTFBMS = &value
		}
		results = append(results, result)
	}
	return results, mapErr(rows.Err())
}
