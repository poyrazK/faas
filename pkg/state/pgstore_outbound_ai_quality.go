package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	_ OutboundAIQualityStore   = (*PgStore)(nil)
	_ OutboundAICallStatsStore = (*PgStore)(nil)
)

// GetOutboundAICallStats reads the latest logged calls through the
// request log's (integration, time) index and summarises them by model.
func (s *PgStore) GetOutboundAICallStats(ctx context.Context, accountID, integrationID string) ([]OutboundAICallStats, int64, error) {
	if err := s.requireOutboundAIIntegration(ctx, accountID, integrationID); err != nil {
		return nil, 0, err
	}
	rows, err := s.pool.Query(ctx, `
		WITH recent AS (
		    SELECT model, status, outcome, ttfb_ms, duration_ms
		      FROM outbound_ai_request_log
		     WHERE integration_id = $1 AND created_at >= now() - make_interval(hours => $2::int)
		     ORDER BY created_at DESC, id DESC
		     LIMIT $3
		)
		SELECT model, count(*), count(*) FILTER (WHERE outcome = 'error' OR status >= 400),
		       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY ttfb_ms))::bigint,
		       round(percentile_cont(0.95) WITHIN GROUP (ORDER BY ttfb_ms))::bigint,
		       round(percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms))::bigint,
		       round(percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms))::bigint
		  FROM recent
		 GROUP BY model
		 ORDER BY count(*) DESC, model`,
		mustPgUUID(integrationID), api.OutboundAICallStatsWindowHours, api.OutboundAICallStatsCallsMax)
	if err != nil {
		return nil, 0, mapErr(err)
	}
	defer rows.Close()
	stats := make([]OutboundAICallStats, 0)
	var total int64
	for rows.Next() {
		var item OutboundAICallStats
		if err := rows.Scan(&item.Model, &item.Calls, &item.ErrorCalls, &item.TTFBP50MS, &item.TTFBP95MS, &item.DurationP50MS,
			&item.DurationP95MS); err != nil {
			return nil, 0, mapErr(err)
		}
		total += item.Calls
		stats = append(stats, item)
	}
	return stats, total, mapErr(rows.Err())
}

// outboundAIUsageOverflowModel is the model outboundd's daily rollup counts
// a day's models past its cap under.
const outboundAIUsageOverflowModel = "other"

// ListOutboundAIQualityAlerts lists the drift alerts of an account's
// integration, newest first.
func (s *PgStore) ListOutboundAIQualityAlerts(ctx context.Context, accountID, integrationID string, limit int) ([]OutboundAIQualityAlert, error) {
	if err := s.requireOutboundAIIntegration(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, integration_id::text, to_char(usage_date, 'YYYY-MM-DD'), model, metric, current_value, baseline_value,
		       current_count, baseline_count, detected_at
		  FROM outbound_ai_quality_alerts
		 WHERE integration_id = $1
		 ORDER BY detected_at DESC, id
		 LIMIT $2`, mustPgUUID(integrationID), boundedPage(limit, api.OutboundAIQualityAlertsPageMax))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	alerts := make([]OutboundAIQualityAlert, 0)
	for rows.Next() {
		var alert OutboundAIQualityAlert
		if err := rows.Scan(&alert.ID, &alert.IntegrationID, &alert.UsageDate, &alert.Model, &alert.Metric, &alert.CurrentValue,
			&alert.BaselineValue, &alert.CurrentCount, &alert.BaselineCount, &alert.DetectedAt); err != nil {
			return nil, mapErr(err)
		}
		alert.DetectedAt = alert.DetectedAt.UTC()
		alerts = append(alerts, alert)
	}
	return alerts, mapErr(rows.Err())
}

// requireOutboundAIIntegration reports ErrNotFound for an integration that
// is not the account's.
func (s *PgStore) requireOutboundAIIntegration(ctx context.Context, accountID, integrationID string) error {
	if _, err := uuid.Parse(integrationID); err != nil {
		return ErrNotFound
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM outbound_integrations WHERE account_id = $1 AND id = $2 AND provider_auth_mode = 'managed')`,
		mustPgUUID(accountID), mustPgUUID(integrationID)).Scan(&exists); err != nil {
		return mapErr(err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// SetOutboundAIRequestFeedback rates a logged call and moves its daily
// rollup row from the earlier rating to this one, as outboundd does for
// an app's rating.
func (s *PgStore) SetOutboundAIRequestFeedback(ctx context.Context, accountID, integrationID, requestID, rating, label string) error {
	if !api.ValidOutboundAIFeedback(rating, label) {
		return ErrInvalidArgument
	}
	if _, err := uuid.Parse(requestID); err != nil {
		return ErrNotFound
	}
	if err := s.requireOutboundAIIntegration(ctx, accountID, integrationID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var previous *string
	var caller, model string
	var day time.Time
	err = tx.QueryRow(ctx, `
		SELECT feedback, caller, model, (created_at AT TIME ZONE 'UTC')::date
		  FROM outbound_ai_request_log
		 WHERE id = $1 AND integration_id = $2
		   FOR UPDATE`, mustPgUUID(requestID), mustPgUUID(integrationID)).Scan(&previous, &caller, &model, &day)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return mapErr(err)
	}
	count := func(value *string, want string) int64 {
		if value != nil && *value == want {
			return 1
		}
		return 0
	}
	good := count(&rating, api.OutboundAIFeedbackGood) - count(previous, api.OutboundAIFeedbackGood)
	bad := count(&rating, api.OutboundAIFeedbackBad) - count(previous, api.OutboundAIFeedbackBad)
	batch := &pgx.Batch{}
	batch.Queue(`
		UPDATE outbound_ai_request_log SET feedback = $2, feedback_label = NULLIF($3, ''), feedback_at = now()
		 WHERE id = $1`, mustPgUUID(requestID), rating, label)
	if good != 0 || bad != 0 {
		batch.Queue(`
			UPDATE outbound_ai_usage_daily usage
			   SET good_feedback = usage.good_feedback + $5, bad_feedback = usage.bad_feedback + $6, updated_at = now()
			 WHERE usage.integration_id = $1 AND usage.usage_date = $2 AND usage.caller = $3
			   AND usage.model = CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_usage_daily own
			                                        WHERE own.integration_id = $1 AND own.usage_date = $2
			                                          AND own.caller = $3 AND own.model = $4)
			                          THEN $4 ELSE $7 END`,
			mustPgUUID(integrationID), day, caller, model, good, bad, outboundAIUsageOverflowModel)
	}
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
