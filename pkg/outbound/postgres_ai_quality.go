package outbound

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ AIFeedbackRecorder = (*PostgresBackend)(nil)

// RecordAIFeedback stores a rating on the call's request-log entry and
// moves the call's daily rollup row from the earlier rating, if any, to
// this one (ADR 20261021171712). The row is the call's day, caller and
// model, or the day's overflow model when the model had no row of its own.
func (b *PostgresBackend) RecordAIFeedback(ctx context.Context, feedback AIFeedback) error {
	if err := validateAIFeedback(feedback); err != nil {
		return err
	}
	integrationID, callID := uuid.MustParse(feedback.IntegrationID), uuid.MustParse(feedback.CallID)
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous *string
	var model string
	var day time.Time
	err = tx.QueryRow(ctx, `
		SELECT feedback, model, (created_at AT TIME ZONE 'UTC')::date
		  FROM outbound_ai_request_log
		 WHERE id = $1 AND integration_id = $2 AND caller = $3
		   FOR UPDATE`, callID, integrationID, feedback.Caller).Scan(&previous, &model, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAICallNotFound
	}
	if err != nil {
		return err
	}
	rating := func(value *string, want string) int64 {
		if value != nil && *value == want {
			return 1
		}
		return 0
	}
	good := rating(&feedback.Rating, "good") - rating(previous, "good")
	bad := rating(&feedback.Rating, "bad") - rating(previous, "bad")
	batch := &pgx.Batch{}
	batch.Queue(`
		UPDATE outbound_ai_request_log SET feedback = $2, feedback_label = NULLIF($3, ''), feedback_at = now()
		 WHERE id = $1`, callID, feedback.Rating, feedback.Label)
	if good != 0 || bad != 0 {
		batch.Queue(`
			UPDATE outbound_ai_usage_daily usage
			   SET good_feedback = usage.good_feedback + $5, bad_feedback = usage.bad_feedback + $6, updated_at = now()
			 WHERE usage.integration_id = $1 AND usage.usage_date = $2 AND usage.caller = $3
			   AND usage.model = CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_usage_daily own
			                                        WHERE own.integration_id = $1 AND own.usage_date = $2
			                                          AND own.caller = $3 AND own.model = $4)
			                          THEN $4 ELSE $7 END`,
			integrationID, day, feedback.Caller, model, good, bad, aiUsageOverflowModel)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ AIOnlineEvaluationStore = (*PostgresBackend)(nil)

// ClaimAIOnlineEvaluations claims the oldest pending calls, and calls whose
// claim went stale, with their captured bodies and the integration's judge.
func (b *PostgresBackend) ClaimAIOnlineEvaluations(ctx context.Context, limit int) ([]AIOnlineEvaluationJob, error) {
	rows, err := b.pool.Query(ctx, `
		WITH claimed AS (
		    UPDATE outbound_ai_request_log log SET evaluation_state = 'running', evaluation_claimed_at = now()
		     WHERE log.id IN (SELECT candidate.id FROM outbound_ai_request_log candidate
		                       WHERE candidate.evaluation_state = 'pending'
		                          OR (candidate.evaluation_state = 'running'
		                              AND candidate.evaluation_claimed_at < now() - make_interval(mins => $2::int))
		                       ORDER BY candidate.created_at LIMIT $1 FOR UPDATE SKIP LOCKED)
		    RETURNING log.id, log.integration_id
		)
		SELECT claimed.id, claimed.integration_id, integration.account_id, integration.ai_online_eval_judge_integration_id,
		       COALESCE(integration.ai_online_eval_model, ''), COALESCE(integration.ai_online_eval_rubric, ''),
		       content.log_id IS NOT NULL, COALESCE(content.request_body, ''::bytea), COALESCE(content.response_body, ''::bytea),
		       COALESCE(content.response_content_type, '')
		  FROM claimed
		  JOIN outbound_integrations integration ON integration.id = claimed.integration_id
		  LEFT JOIN outbound_ai_request_content content ON content.log_id = claimed.id`,
		limit, api.OutboundAIOnlineEvaluationClaimMinutes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []AIOnlineEvaluationJob
	for rows.Next() {
		var logID, integrationID, accountID uuid.UUID
		var judgeID *uuid.UUID
		var job AIOnlineEvaluationJob
		if err := rows.Scan(&logID, &integrationID, &accountID, &judgeID, &job.JudgeModel, &job.Rubric, &job.Captured,
			&job.RequestBody, &job.ResponseBody, &job.ResponseContentType); err != nil {
			return nil, err
		}
		job.LogID, job.IntegrationID, job.AccountID = logID.String(), integrationID.String(), accountID.String()
		if judgeID != nil {
			job.JudgeIntegrationID = judgeID.String()
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

// FinishAIOnlineEvaluation stores a claimed call's score and adds it to the
// call's daily rollup row. A call another worker finished first is left as
// it is, so a score counts once.
func (b *PostgresBackend) FinishAIOnlineEvaluation(ctx context.Context, job AIOnlineEvaluationJob, score int) error {
	if score < 0 || score > 5 {
		return ErrInvalidIntegration
	}
	logID, err := uuid.Parse(job.LogID)
	if err != nil {
		return ErrInvalidIntegration
	}
	_, err = b.pool.Exec(ctx, `
		WITH finished AS (
		    UPDATE outbound_ai_request_log
		       SET evaluation_state = CASE WHEN $2::int > 0 THEN 'done' ELSE 'error' END,
		           evaluation_score = NULLIF($2::int, 0)::smallint, evaluation_claimed_at = NULL
		     WHERE id = $1 AND evaluation_state = 'running'
		    RETURNING integration_id, caller, model, (created_at AT TIME ZONE 'UTC')::date AS usage_date
		)
		UPDATE outbound_ai_usage_daily usage
		   SET evaluated_requests = usage.evaluated_requests + CASE WHEN $2::int > 0 THEN 1 ELSE 0 END,
		       evaluation_score_sum = usage.evaluation_score_sum + $2::int,
		       evaluation_errors = usage.evaluation_errors + CASE WHEN $2::int > 0 THEN 0 ELSE 1 END,
		       updated_at = now()
		  FROM finished
		 WHERE usage.integration_id = finished.integration_id AND usage.usage_date = finished.usage_date
		   AND usage.caller = finished.caller
		   AND usage.model = CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_usage_daily own
		                                        WHERE own.integration_id = finished.integration_id
		                                          AND own.usage_date = finished.usage_date AND own.caller = finished.caller
		                                          AND own.model = finished.model)
		                          THEN finished.model ELSE $3 END`,
		logID, score, aiUsageOverflowModel)
	return err
}
