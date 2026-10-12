package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// RecordAIUsage settles one AI call (ADR-988). The caller/model rollup is
// written before the admission counter so this transaction takes locks in the
// same order as admission and integration deletion: integration, then state.
func (b *PostgresBackend) RecordAIUsage(ctx context.Context, record AIUsageRecord) error {
	_, err := b.RecordAIUsageAlerts(ctx, record)
	return err
}

// aiSettlementLimits are the stored limits a settlement compares against
// to find budget alerts (ADR-991).
type aiSettlementLimits struct {
	accountID                           uuid.UUID
	name                                string
	dailyTokens, dailyCost, monthlyCost int64
	// Per-end-user UTC-day limits (ADR-993).
	userTokens, userCost int64
}

// RecordAIUsageAlerts settles one AI call and, in the same transaction,
// records the budget alerts it reached with their webhooks and the call's
// request-log entry (ADR-991).
func (b *PostgresBackend) RecordAIUsageAlerts(ctx context.Context, record AIUsageRecord) ([]AIBudgetAlert, error) {
	if err := validateAIUsageRecord(record); err != nil {
		return nil, err
	}
	integrationID, err := uuid.Parse(record.IntegrationID)
	if err != nil {
		return nil, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	if record.Caller != AIUsageCallerRuns && !validUUID(record.Caller) {
		return nil, fmt.Errorf("%w: AI usage caller must be an app UUID or %q", ErrInvalidIntegration, AIUsageCallerRuns)
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := insertAIUsageRollup(ctx, tx, integrationID, record); err != nil {
		return nil, err
	}
	user, err := insertAIUserUsageRollup(ctx, tx, integrationID, record)
	if err != nil {
		return nil, err
	}
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return nil, err
	}
	var limits aiSettlementLimits
	if err := tx.QueryRow(ctx, `
		SELECT account_id, name, COALESCE(ai_daily_token_limit, 0),
		       COALESCE(ai_daily_cost_limit_millicents, 0), COALESCE(ai_monthly_cost_limit_millicents, 0),
		       COALESCE(ai_user_daily_token_limit, 0), COALESCE(ai_user_daily_cost_limit_millicents, 0)
		  FROM outbound_integrations WHERE id = $1`, integrationID).
		Scan(&limits.accountID, &limits.name, &limits.dailyTokens, &limits.dailyCost, &limits.monthlyCost,
			&limits.userTokens, &limits.userCost); err != nil {
		return nil, err
	}
	crossings, err := settleAIUsageCounters(ctx, tx, integrationID, record, limits, now)
	if err != nil {
		return nil, err
	}
	crossings = append(crossings, userBudgetCrossings(user.endUser, utcDayStart(now), positiveLimit(limits.userTokens),
		positiveLimit(limits.userCost), user.tokens, user.cost, record.Usage.InputTokens+record.Usage.OutputTokens, record.CostMillicents)...)
	alerts, err := recordAIBudgetAlerts(ctx, tx, integrationID, limits, record.Caller, aiBudgetAlertsFor(record.IntegrationID, crossings, now.UTC()))
	if err != nil {
		return nil, err
	}
	if err := insertAIRequestLog(ctx, tx, integrationID, record); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return alerts, nil
}

func insertAIUsageRollup(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, record AIUsageRecord) error {
	var errorRequests, unknownRequests int64
	switch record.Outcome {
	case AIUsageOutcomeError:
		errorRequests = 1
	case AIUsageOutcomeUnknown:
		unknownRequests = 1
	}
	usage := record.Usage
	// A provider-chosen model name may only add a new row while the day has
	// fewer than the capped number of distinct models; later ones aggregate
	// under "other" so a customer origin cannot grow the table without bound.
	_, err := tx.Exec(ctx, `
		INSERT INTO outbound_ai_usage_daily AS usage
		    (integration_id, usage_date, caller, model, requests, error_requests, usage_unknown_requests,
		     input_tokens, cached_input_tokens, cache_write_tokens, output_tokens, cost_millicents)
		SELECT $1, day.today, $2,
		       CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_usage_daily existing
		                          WHERE existing.integration_id = $1 AND existing.usage_date = day.today
		                            AND existing.model = $3)
		              OR (SELECT count(DISTINCT existing.model) FROM outbound_ai_usage_daily existing
		                   WHERE existing.integration_id = $1 AND existing.usage_date = day.today) < $4
		            THEN $3 ELSE $5 END,
		       1, $6, $7, $8, $9, $10, $11, $12
		  FROM (SELECT (now() AT TIME ZONE 'UTC')::date AS today) day
		ON CONFLICT (integration_id, usage_date, caller, model) DO UPDATE SET
		    requests = usage.requests + 1,
		    error_requests = usage.error_requests + EXCLUDED.error_requests,
		    usage_unknown_requests = usage.usage_unknown_requests + EXCLUDED.usage_unknown_requests,
		    input_tokens = usage.input_tokens + EXCLUDED.input_tokens,
		    cached_input_tokens = usage.cached_input_tokens + EXCLUDED.cached_input_tokens,
		    cache_write_tokens = usage.cache_write_tokens + EXCLUDED.cache_write_tokens,
		    output_tokens = usage.output_tokens + EXCLUDED.output_tokens,
		    cost_millicents = usage.cost_millicents + EXCLUDED.cost_millicents,
		    updated_at = now()`,
		integrationID, record.Caller, usage.Model, api.OutboundAIUsageModelsPerDay, aiUsageOverflowModel,
		errorRequests, unknownRequests, usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteTokens,
		usage.OutputTokens, record.CostMillicents)
	return err
}

// settleAIUsageCounters adds the call to the admission and binding counters
// and returns each limited budget's before and after values.
func settleAIUsageCounters(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, record AIUsageRecord,
	limits aiSettlementLimits, now time.Time) ([]aiBudgetCrossing, error) {
	budgetTokens := record.Usage.BudgetTokens()
	cost := record.CostMillicents
	var crossings []aiBudgetCrossing
	// The counters follow admission's UTC-day and calendar-month resets. A
	// call that straddles midnight counts toward the day it settles in.
	if budgetTokens > 0 || cost > 0 {
		var dailyTokens, dailyCost, monthlyCost int64
		err := tx.QueryRow(ctx, `
			UPDATE outbound_admission_state
			   SET daily_request_count = CASE WHEN daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                  THEN daily_request_count ELSE 0 END,
			       daily_ai_token_count = CASE WHEN daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                   THEN daily_ai_token_count + $2 ELSE $2 END,
			       daily_ai_cost_millicents = CASE WHEN daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                       THEN daily_ai_cost_millicents + $3 ELSE $3 END,
			       monthly_ai_cost_millicents = CASE WHEN monthly_usage_month = (date_trunc('month', now() AT TIME ZONE 'UTC'))::date
			                                         THEN monthly_ai_cost_millicents + $3 ELSE $3 END,
			       daily_usage_date = (now() AT TIME ZONE 'UTC')::date,
			       monthly_usage_month = (date_trunc('month', now() AT TIME ZONE 'UTC'))::date
			 WHERE integration_id = $1
			RETURNING daily_ai_token_count, daily_ai_cost_millicents, monthly_ai_cost_millicents`,
			integrationID, budgetTokens, cost).Scan(&dailyTokens, &dailyCost, &monthlyCost)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			day, month := utcDayStart(now), utcMonthStart(now)
			crossings = append(crossings,
				aiBudgetCrossing{api.OutboundAIBudgetDailyTokens, "", day, positiveLimit(limits.dailyTokens), dailyTokens - budgetTokens, dailyTokens, ""},
				aiBudgetCrossing{api.OutboundAIBudgetDailyCost, "", day, positiveLimit(limits.dailyCost), dailyCost - cost, dailyCost, ""},
				aiBudgetCrossing{api.OutboundAIBudgetMonthlyCost, "", month, positiveLimit(limits.monthlyCost), monthlyCost - cost, monthlyCost, ""})
		}
	}
	// ADR-990: the app's own binding counter, when the app has a binding row.
	// Operator attachments and Runs have none, so nothing is updated.
	if budgetTokens > 0 && record.Caller != AIUsageCallerRuns {
		appID := uuid.MustParse(record.Caller)
		var bindingTokens, bindingLimit int64
		err := tx.QueryRow(ctx, `
			UPDATE outbound_app_binding_usage usage
			   SET daily_request_count = CASE WHEN usage.daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                  THEN usage.daily_request_count ELSE 0 END,
			       daily_ai_token_count = CASE WHEN usage.daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                   THEN usage.daily_ai_token_count + $3 ELSE $3 END,
			       daily_usage_date = (now() AT TIME ZONE 'UTC')::date,
			       -- ADR-991: tokens count toward the minute they settle in.
			       minute_request_count = CASE WHEN usage.minute_window_start = date_trunc('minute', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
			                                   THEN usage.minute_request_count ELSE 0 END,
			       minute_ai_token_count = CASE WHEN usage.minute_window_start = date_trunc('minute', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
			                                    THEN usage.minute_ai_token_count + $3 ELSE $3 END,
			       minute_window_start = date_trunc('minute', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
			  FROM outbound_app_bindings binding
			 WHERE usage.integration_id = $1 AND usage.app_id = $2
			   AND binding.integration_id = usage.integration_id AND binding.app_id = usage.app_id
			RETURNING usage.daily_ai_token_count, COALESCE(binding.ai_daily_token_limit, 0)`,
			integrationID, appID, budgetTokens).Scan(&bindingTokens, &bindingLimit)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			crossings = append(crossings, aiBudgetCrossing{api.OutboundAIBudgetAppDailyTokens, record.Caller, utcDayStart(now),
				positiveLimit(bindingLimit), bindingTokens - budgetTokens, bindingTokens, ""})
		}
	}
	return crossings, nil
}

// recordAIBudgetAlerts stores first-time alerts and enqueues one
// outbound.ai_budget.threshold webhook per affected app: the app itself for
// an app budget, every bound app for an integration budget, and the calling
// app for an end user's budget (ADR-993).
func recordAIBudgetAlerts(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, limits aiSettlementLimits, caller string,
	alerts []AIBudgetAlert) ([]AIBudgetAlert, error) {
	var recorded []AIBudgetAlert
	for _, alert := range alerts {
		var appID *uuid.UUID
		if alert.AppID != "" {
			parsed := uuid.MustParse(alert.AppID)
			appID = &parsed
		}
		var id uuid.UUID
		var crossedAt time.Time
		err := tx.QueryRow(ctx, `
			INSERT INTO outbound_ai_budget_alerts
			    (integration_id, account_id, budget, app_id, end_user, period_start, threshold_percent, limit_value, used_value)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9)
			ON CONFLICT DO NOTHING
			RETURNING id, crossed_at`, integrationID, limits.accountID, alert.Budget, appID, alert.EndUser, alert.PeriodStart,
			alert.ThresholdPercent, alert.Limit, alert.Used).Scan(&id, &crossedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		alert.ID, alert.CrossedAt = id.String(), crossedAt.UTC()
		notify := alert.AppID
		if alert.EndUser != "" {
			// Runs have no app to notify; the alert is still listed.
			notify = ""
			if caller != AIUsageCallerRuns {
				notify = caller
			}
		}
		if alert.EndUser == "" || notify != "" {
			if err := enqueueAIBudgetWebhooks(ctx, tx, integrationID, limits, alert, notify); err != nil {
				return nil, err
			}
		}
		recorded = append(recorded, alert)
	}
	return recorded, nil
}

// enqueueAIBudgetWebhooks notifies the bound app notifyApp, or every bound
// app when it is empty.
func enqueueAIBudgetWebhooks(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, limits aiSettlementLimits, alert AIBudgetAlert, notifyApp string) error {
	payload := api.OutboundAIBudgetThresholdWebhookPayload{
		AlertID: alert.ID, IntegrationID: alert.IntegrationID, IntegrationName: limits.name, Budget: alert.Budget,
		PeriodStart: alert.PeriodStart.Format("2006-01-02"), ThresholdPercent: int32(alert.ThresholdPercent),
		Limit: alert.Limit, Used: alert.Used, CrossedAt: alert.CrossedAt,
	}
	if alert.AppID != "" {
		payload.AppID = &alert.AppID
	}
	if alert.EndUser != "" {
		payload.EndUser = &alert.EndUser
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	// The source ID is derived per app so one alert fans out to several
	// apps under the outbox's (event, source_id) key.
	_, err = tx.Exec(ctx, `
		INSERT INTO app_webhook_event_outbox (account_id, app_id, event, source_id, payload, recipient_webhook_ids)
		SELECT $1::uuid, target.app_id, 'outbound.ai_budget.threshold', md5($2::text || ':' || target.app_id::text)::uuid,
		       $3::jsonb, array_agg(hook.id ORDER BY hook.id)
		  FROM (SELECT binding.app_id FROM outbound_app_bindings binding
		         WHERE binding.integration_id = $4 AND ($5::uuid IS NULL OR binding.app_id = $5::uuid)) target
		  JOIN app_webhooks hook ON hook.account_id = $1::uuid AND hook.app_id = target.app_id
		   AND hook.scope = 'app' AND hook.enabled
		   AND (cardinality(hook.event_filter) = 0 OR 'outbound.ai_budget.threshold' = ANY(hook.event_filter))
		 GROUP BY target.app_id
		ON CONFLICT (event, source_id) DO NOTHING`,
		limits.accountID, alert.ID, string(encoded), integrationID, nullableAppID(notifyApp))
	return err
}

func nullableAppID(appID string) *uuid.UUID {
	if appID == "" {
		return nil
	}
	parsed := uuid.MustParse(appID)
	return &parsed
}

func insertAIRequestLog(ctx context.Context, tx pgx.Tx, integrationID uuid.UUID, record AIUsageRecord) error {
	if record.Log == nil {
		return nil
	}
	var routeID *uuid.UUID
	if record.Log.RouteID != "" {
		parsed := uuid.MustParse(record.Log.RouteID)
		routeID = &parsed
	}
	usage := record.Usage
	_, err := tx.Exec(ctx, `
		INSERT INTO outbound_ai_request_log
		    (integration_id, caller, route_id, model, status, outcome, input_tokens, cached_input_tokens,
		     cache_write_tokens, output_tokens, cost_millicents, duration_ms, ttfb_ms, attempts, end_user)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NULLIF($15, ''))`,
		integrationID, record.Caller, routeID, usage.Model, record.Log.Status, string(record.Outcome), usage.InputTokens,
		usage.CachedInputTokens, usage.CacheWriteTokens, usage.OutputTokens, record.CostMillicents,
		record.Log.DurationMS, record.Log.TTFBMS, record.Log.Attempts, record.EndUser)
	return err
}

// PruneAIUsage deletes UTC-day AI usage rollups and budget alerts older
// than the rollup retention, and request-log rows older than theirs. It is
// idempotent, so every outboundd replica may run it.
func (b *PostgresBackend) PruneAIUsage(ctx context.Context) (int64, error) {
	var total int64
	for _, statement := range []struct {
		sql  string
		days int
	}{
		{`DELETE FROM outbound_ai_usage_daily WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUsageRetentionDays},
		{`DELETE FROM outbound_ai_budget_alerts WHERE period_start < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUsageRetentionDays},
		{`DELETE FROM outbound_ai_request_log WHERE created_at < now() - make_interval(days => $1::int)`, api.OutboundAIRequestLogRetentionDays},
		{`DELETE FROM outbound_ai_user_usage_daily WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUserUsageRetentionDays},
		// ADR-993: end users' alerts follow the per-user rollup retention.
		{`DELETE FROM outbound_ai_budget_alerts WHERE end_user IS NOT NULL AND period_start < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUserUsageRetentionDays},
	} {
		command, err := b.pool.Exec(ctx, statement.sql, statement.days)
		if err != nil {
			return total, err
		}
		total += command.RowsAffected()
	}
	return total, nil
}

var _ AIUsageBackend = (*PostgresBackend)(nil)
var _ AIBudgetAlertRecorder = (*PostgresBackend)(nil)
