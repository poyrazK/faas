package outbound

import (
	"context"
	"errors"
	"regexp"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ AIReplayStore = (*PostgresBackend)(nil)

var replayErrorCode = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// ClaimAIReplay fails replays that stopped mid-run, then claims the oldest
// pending replay with its captured call (ADR-997). A replay whose capture
// is gone or cannot be resent fails without a claim.
func (b *PostgresBackend) ClaimAIReplay(ctx context.Context) (AIReplayJob, bool, error) {
	if _, err := b.pool.Exec(ctx, `
		UPDATE outbound_ai_replays SET state = 'failed', error_code = 'outbound_replay_interrupted', finished_at = now()
		 WHERE state = 'running' AND started_at < now() - make_interval(mins => $1::int)`,
		api.OutboundAIReplayInterruptedAfterMinutes); err != nil {
		return AIReplayJob{}, false, err
	}
	var id, accountID, integrationID, logID uuid.UUID
	var targets []uuid.UUID
	var models []string
	err := b.pool.QueryRow(ctx, `
		UPDATE outbound_ai_replays replay SET state = 'running', started_at = now()
		 WHERE replay.id = (SELECT pending.id FROM outbound_ai_replays pending
		                     WHERE pending.state = 'pending' ORDER BY pending.created_at
		                     LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING replay.id, replay.account_id, replay.integration_id, replay.log_id,
		          replay.target_integration_ids, replay.target_models`).
		Scan(&id, &accountID, &integrationID, &logID, &targets, &models)
	if errors.Is(err, pgx.ErrNoRows) {
		return AIReplayJob{}, false, nil
	}
	if err != nil {
		return AIReplayJob{}, false, err
	}
	job := AIReplayJob{ID: id.String(), AccountID: accountID.String(), IntegrationID: integrationID.String()}
	for index, target := range targets {
		job.Targets = append(job.Targets, AIRouteTarget{IntegrationID: target.String(), Model: models[index]})
	}
	var truncated bool
	err = b.pool.QueryRow(ctx, `
		SELECT log.model, COALESCE(content.request_path, ''), content.request_body, content.request_truncated,
		       content.response_body, COALESCE(content.response_content_type, '')
		  FROM outbound_ai_request_log log
		  JOIN outbound_ai_request_content content ON content.log_id = log.id
		 WHERE log.id = $1`, logID).
		Scan(&job.SourceModel, &job.RequestPath, &job.RequestBody, &truncated, &job.ResponseBody, &job.ResponseContentType)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return AIReplayJob{}, false, b.FinishAIReplay(ctx, job, "", nil, "outbound_replay_capture_gone")
	case err != nil:
		return AIReplayJob{}, false, err
	case truncated || job.RequestPath == "":
		return AIReplayJob{}, false, b.FinishAIReplay(ctx, job, "", nil, "outbound_replay_unsupported")
	}
	return job, true, nil
}

// FinishAIReplay stores a replay's results and ends it: done, or failed
// with errorCode.
func (b *PostgresBackend) FinishAIReplay(ctx context.Context, job AIReplayJob, sourceOutputText string, results []AIReplayResult, errorCode string) error {
	replayID, err := uuid.Parse(job.ID)
	if err != nil {
		return err
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for position, result := range results {
		if err := insertAIReplayResult(ctx, tx, replayID, position, result); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE outbound_ai_replays
		   SET state = CASE WHEN $2 = '' THEN 'done' ELSE 'failed' END, error_code = NULLIF($2, ''),
		       source_output_text = NULLIF($3, ''), finished_at = now()
		 WHERE id = $1 AND state = 'running'`,
		replayID, errorCode, truncateUTF8(sourceOutputText, api.OutboundAIReplayOutputTextMaxBytes)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func insertAIReplayResult(ctx context.Context, tx pgx.Tx, replayID uuid.UUID, position int, result AIReplayResult) error {
	target, err := uuid.Parse(result.IntegrationID)
	if err != nil {
		return err
	}
	var status *int
	if result.Status >= 100 && result.Status <= 599 {
		status = &result.Status
	}
	var outcome *string
	if result.Outcome != "" {
		value := string(result.Outcome)
		outcome = &value
	}
	errorCode := result.ErrorCode
	if errorCode != "" && !replayErrorCode.MatchString(errorCode) {
		errorCode = "provider_error"
	}
	var ttfb *int64
	if result.TTFBMS != nil {
		value := min(max(*result.TTFBMS, 0), 1<<31-1)
		ttfb = &value
	}
	contentType := result.ResponseContentType
	if sanitizeAIModel(contentType) != contentType || len(contentType) > aiContentTypeMaxBytes {
		contentType = ""
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO outbound_ai_replay_results
		    (replay_id, position, target_integration_id, model, status, outcome, error_code, input_tokens, output_tokens,
		     cost_millicents, duration_ms, ttfb_ms, response_body, response_truncated, response_content_type, output_text)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5, $6, NULLIF($7, ''), $8, $9, $10, $11, $12, $13, $14, NULLIF($15, ''), $16)`,
		replayID, position, target, sanitizeAIModel(result.Model), status, outcome, errorCode,
		result.Usage.InputTokens, result.Usage.OutputTokens, result.CostMillicents,
		min(max(result.DurationMS, 0), 1<<31-1), ttfb, nonNilBytes(result.ResponseBody), result.ResponseTruncated,
		contentType, truncateUTF8(result.OutputText, api.OutboundAIReplayOutputTextMaxBytes))
	return err
}
