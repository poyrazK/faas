package outbound

import (
	"context"
	"encoding/json"
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
	integrationID, err := uuid.Parse(feedback.IntegrationID)
	if err != nil {
		return ErrAICallNotFound
	}
	callID := uuid.MustParse(feedback.CallID)
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

var _ AIQualityDriftStore = (*PostgresBackend)(nil)

// AIQualityWindows sums each quality-alerting integration's models over
// their UTC day so far and the baseline days before it. Internal callers
// and the overflow model are left out: neither is one app model's
// quality.
func (b *PostgresBackend) AIQualityWindows(ctx context.Context) ([]AIQualityWindow, error) {
	rows, err := b.pool.Query(ctx, `
		WITH day AS (SELECT (now() AT TIME ZONE 'UTC')::date AS today)
		SELECT usage.integration_id, integration.account_id, integration.name, usage.model, day.today,
		       sum(usage.requests - usage.error_requests - usage.usage_unknown_requests - usage.cached_requests)
		           FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.truncated_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.refused_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.empty_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.invalid_json_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.tool_error_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.good_feedback) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.bad_feedback) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.evaluated_requests) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.evaluation_score_sum) FILTER (WHERE usage.usage_date = day.today),
		       sum(usage.requests - usage.error_requests - usage.usage_unknown_requests - usage.cached_requests)
		           FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.truncated_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.refused_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.empty_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.invalid_json_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.tool_error_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.good_feedback) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.bad_feedback) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.evaluated_requests) FILTER (WHERE usage.usage_date < day.today),
		       sum(usage.evaluation_score_sum) FILTER (WHERE usage.usage_date < day.today)
		  FROM day, outbound_ai_usage_daily usage
		  JOIN outbound_integrations integration ON integration.id = usage.integration_id
		 WHERE integration.ai_quality_alerts AND integration.ai_provider IS NOT NULL
		   AND usage.usage_date BETWEEN day.today - $1::int AND day.today
		   AND usage.caller NOT IN ('replay', 'moderation', 'evaluation') AND usage.model <> $2
		 GROUP BY usage.integration_id, integration.account_id, integration.name, usage.model, day.today`,
		api.OutboundAIQualityDriftBaselineDays, aiUsageOverflowModel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var windows []AIQualityWindow
	for rows.Next() {
		var window AIQualityWindow
		var integrationID, accountID uuid.UUID
		var sums [20]*int64
		targets := []any{&integrationID, &accountID, &window.IntegrationName, &window.Model, &window.UsageDate}
		for index := range sums {
			targets = append(targets, &sums[index])
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		value := func(index int) int64 {
			if sums[index] == nil {
				return 0
			}
			return *sums[index]
		}
		totals := func(offset int) AIQualityTotals {
			return AIQualityTotals{Answered: value(offset), Truncated: value(offset + 1), Refused: value(offset + 2),
				Empty: value(offset + 3), InvalidJSON: value(offset + 4), ToolError: value(offset + 5),
				GoodFeedback: value(offset + 6), BadFeedback: value(offset + 7), Evaluated: value(offset + 8),
				ScoreSum: value(offset + 9)}
		}
		window.IntegrationID, window.AccountID = integrationID.String(), accountID.String()
		window.Today, window.Baseline = totals(0), totals(10)
		windows = append(windows, window)
	}
	return windows, rows.Err()
}

// RecordAIQualityAlert stores a first-time alert and enqueues one
// outbound.ai_quality.drift webhook for every bound app, in one
// transaction.
func (b *PostgresBackend) RecordAIQualityAlert(ctx context.Context, alert AIQualityAlert) (bool, error) {
	integrationID, err := uuid.Parse(alert.IntegrationID)
	if err != nil {
		return false, ErrInvalidIntegration
	}
	accountID, err := uuid.Parse(alert.AccountID)
	if err != nil {
		return false, ErrInvalidIntegration
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var id uuid.UUID
	var detectedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_quality_alerts
		    (integration_id, account_id, usage_date, model, metric, current_value, baseline_value, current_count, baseline_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (integration_id, usage_date, model, metric) DO NOTHING
		RETURNING id, detected_at`, integrationID, accountID, alert.UsageDate, alert.Model, alert.Metric, alert.CurrentValue,
		alert.BaselineValue, alert.CurrentCount, alert.BaselineCount).Scan(&id, &detectedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload, err := json.Marshal(api.OutboundAIQualityDriftWebhookPayload{
		AlertID: id.String(), IntegrationID: alert.IntegrationID, IntegrationName: alert.IntegrationName,
		UsageDate: alert.UsageDate.Format("2006-01-02"), Model: alert.Model, Metric: alert.Metric,
		CurrentValue: alert.CurrentValue, BaselineValue: alert.BaselineValue, CurrentCount: alert.CurrentCount,
		BaselineCount: alert.BaselineCount, DetectedAt: detectedAt.UTC(),
	})
	if err != nil {
		return false, err
	}
	// The source ID is derived per app so one alert fans out to several
	// apps under the outbox's (event, source_id) key.
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_webhook_event_outbox (account_id, app_id, event, source_id, payload, recipient_webhook_ids)
		SELECT $1::uuid, binding.app_id, 'outbound.ai_quality.drift', md5($2::text || ':' || binding.app_id::text)::uuid,
		       $3::jsonb, array_agg(hook.id ORDER BY hook.id)
		  FROM outbound_app_bindings binding
		  JOIN app_webhooks hook ON hook.account_id = $1::uuid AND hook.app_id = binding.app_id
		   AND hook.scope = 'app' AND hook.enabled
		   AND (cardinality(hook.event_filter) = 0 OR 'outbound.ai_quality.drift' = ANY(hook.event_filter))
		 WHERE binding.integration_id = $4
		 GROUP BY binding.app_id
		ON CONFLICT (event, source_id) DO NOTHING`, accountID, id, string(payload), integrationID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}
