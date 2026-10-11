package outbound

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// RecordAIUsage settles one AI call (ADR-979). The caller/model rollup is
// written before the admission counter so this transaction takes locks in the
// same order as admission and integration deletion: integration, then state.
func (b *PostgresBackend) RecordAIUsage(ctx context.Context, record AIUsageRecord) error {
	if err := validateAIUsageRecord(record); err != nil {
		return err
	}
	integrationID, err := uuid.Parse(record.IntegrationID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	if record.Caller != AIUsageCallerRuns && !validUUID(record.Caller) {
		return fmt.Errorf("%w: AI usage caller must be an app UUID or %q", ErrInvalidIntegration, AIUsageCallerRuns)
	}
	var errorRequests, unknownRequests int64
	switch record.Outcome {
	case AIUsageOutcomeError:
		errorRequests = 1
	case AIUsageOutcomeUnknown:
		unknownRequests = 1
	}
	usage := record.Usage
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// A provider-chosen model name may only add a new row while the day has
	// fewer than the capped number of distinct models; later ones aggregate
	// under "other" so a customer origin cannot grow the table without bound.
	if _, err := tx.Exec(ctx, `
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
		usage.OutputTokens, record.CostMillicents); err != nil {
		return err
	}
	// The counters follow admission's UTC-day and calendar-month resets. A
	// call that straddles midnight counts toward the day it settles in.
	budgetTokens := usage.BudgetTokens()
	if budgetTokens > 0 || record.CostMillicents > 0 {
		if _, err := tx.Exec(ctx, `
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
			 WHERE integration_id = $1`, integrationID, budgetTokens, record.CostMillicents); err != nil {
			return err
		}
	}
	// ADR-983: the app's own binding counter, when the app has a binding row.
	// Operator attachments and Runs have none, so nothing is updated.
	if budgetTokens > 0 && record.Caller != AIUsageCallerRuns {
		if _, err := tx.Exec(ctx, `
			UPDATE outbound_app_binding_usage
			   SET daily_request_count = CASE WHEN daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                  THEN daily_request_count ELSE 0 END,
			       daily_ai_token_count = CASE WHEN daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			                                   THEN daily_ai_token_count + $3 ELSE $3 END,
			       daily_usage_date = (now() AT TIME ZONE 'UTC')::date
			 WHERE integration_id = $1 AND app_id = $2`, integrationID, uuid.MustParse(record.Caller), budgetTokens); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// PruneAIUsage deletes UTC-day AI usage rollups older than the retention
// window. It is idempotent, so every outboundd replica may run it.
func (b *PostgresBackend) PruneAIUsage(ctx context.Context) (int64, error) {
	command, err := b.pool.Exec(ctx, `
		DELETE FROM outbound_ai_usage_daily
		 WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUsageRetentionDays)
	if err != nil {
		return 0, err
	}
	return command.RowsAffected(), nil
}

var _ AIUsageBackend = (*PostgresBackend)(nil)
