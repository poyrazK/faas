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

// RecordAIUsage settles one AI call (ADR-988).
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
	// Per-end-user UTC-day limits (ADR-995).
	userTokens, userCost int64
}

// RecordAIUsageAlerts settles one AI call and, in the same transaction,
// records the budget alerts it reached with their webhooks and the call's
// request-log entry (ADR-991).
//
// A call's lease ID is its settlement key (ADR-1120): the first settlement
// inserts it and deletes the lease, so a retried settlement whose first
// attempt committed changes nothing. The transaction takes two pipelined
// round trips before the commit, and the contended rows (the caller/model
// rollup, the admission state and the binding counters) are locked only in
// the second.
func (b *PostgresBackend) RecordAIUsageAlerts(ctx context.Context, record AIUsageRecord) ([]AIBudgetAlert, error) {
	if err := validateAIUsageRecord(record); err != nil {
		return nil, err
	}
	integrationID, err := uuid.Parse(record.IntegrationID)
	if err != nil {
		return nil, fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	if appCaller(record.Caller) && !validUUID(record.Caller) {
		return nil, fmt.Errorf("%w: AI usage caller must be an app UUID, %q or %q", ErrInvalidIntegration, AIUsageCallerRuns, AIUsageCallerReplay)
	}
	var leaseID uuid.UUID
	if record.LeaseID != "" {
		if leaseID, err = uuid.Parse(record.LeaseID); err != nil {
			return nil, fmt.Errorf("%w: AI usage lease id must be a UUID", ErrInvalidIntegration)
		}
	}
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	opened, err := openAISettlement(ctx, tx, integrationID, leaseID, record)
	if err != nil || opened.settled {
		return nil, err
	}
	crossings, err := settleAIUsageRows(ctx, tx, integrationID, leaseID, record, opened)
	if err != nil {
		return nil, err
	}
	alerts, err := recordAIBudgetAlerts(ctx, tx, integrationID, opened.limits, record.Caller,
		aiBudgetAlertsFor(record.IntegrationID, crossings, opened.now.UTC()))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return alerts, nil
}

// openedAISettlement is what a settlement reads and writes before it locks a
// contended row.
type openedAISettlement struct {
	// settled reports that the call's key was already taken: an earlier
	// attempt committed this settlement.
	settled bool
	now     time.Time
	limits  aiSettlementLimits
	logID   uuid.UUID
}

// openAISettlement claims the call's settlement key, reads the integration's
// limits, and inserts the request-log row and the export line (ADR-1340),
// in one round trip.
func openAISettlement(ctx context.Context, tx pgx.Tx, integrationID, leaseID uuid.UUID, record AIUsageRecord) (openedAISettlement, error) {
	batch := &pgx.Batch{}
	if leaseID != uuid.Nil {
		batch.Queue(`
			INSERT INTO outbound_ai_settlements (lease_id, integration_id) VALUES ($1, $2)
			ON CONFLICT (lease_id) DO NOTHING
			RETURNING lease_id`, leaseID, integrationID)
	}
	batch.Queue(`
		SELECT now(), account_id, name, COALESCE(ai_daily_token_limit, 0),
		       COALESCE(ai_daily_cost_limit_millicents, 0), COALESCE(ai_monthly_cost_limit_millicents, 0),
		       COALESCE(ai_user_daily_token_limit, 0), COALESCE(ai_user_daily_cost_limit_millicents, 0)
		  FROM outbound_integrations WHERE id = $1`, integrationID)
	if record.Log != nil {
		queueAIRequestLog(batch, integrationID, record)
	}
	if record.Export != nil {
		queueAILogExport(batch, record.Export)
	}
	results := tx.SendBatch(ctx, batch)
	opened, err := scanOpenedAISettlement(results, leaseID, record)
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	return opened, err
}

func scanOpenedAISettlement(results pgx.BatchResults, leaseID uuid.UUID, record AIUsageRecord) (openedAISettlement, error) {
	var opened openedAISettlement
	if leaseID != uuid.Nil {
		var claimed uuid.UUID
		err := results.QueryRow().Scan(&claimed)
		if errors.Is(err, pgx.ErrNoRows) {
			opened.settled = true
			return opened, nil
		}
		if err != nil {
			return opened, err
		}
	}
	limits := &opened.limits
	if err := results.QueryRow().Scan(&opened.now, &limits.accountID, &limits.name, &limits.dailyTokens, &limits.dailyCost,
		&limits.monthlyCost, &limits.userTokens, &limits.userCost); err != nil {
		return opened, err
	}
	if record.Log != nil {
		if err := results.QueryRow().Scan(&opened.logID); err != nil {
			return opened, err
		}
	}
	if record.Export != nil {
		if _, err := results.Exec(); err != nil {
			return opened, err
		}
	}
	return opened, nil
}

// queueAILogExport adds the call's line to the export outbox the gateways
// read for the app's log drains (ADR-1340).
func queueAILogExport(batch *pgx.Batch, export *AILogExport) {
	attributes, err := json.Marshal(export.Attributes)
	if err != nil || export.Attributes == nil {
		attributes = []byte("{}")
	}
	batch.Queue(`
		INSERT INTO outbound_ai_log_exports (app_id, account_id, line, attributes)
		VALUES ($1, $2, $3, $4::jsonb)`, uuid.MustParse(export.AppID), uuid.MustParse(export.AccountID), export.Line, string(attributes))
}

// settleAIUsageRows adds the call to the caller/model and end-user rollups
// and to the admission and binding counters, deletes its lease, and stores
// its captured content, in one round trip. A canary-tracked route attempt
// adds to its target's per-minute row last (ADR-1340). It returns each
// limited budget's before and after values. The rollup is written before the admission
// counter so the transaction takes locks in the same order as admission and
// integration deletion: integration, then state.
func settleAIUsageRows(ctx context.Context, tx pgx.Tx, integrationID, leaseID uuid.UUID, record AIUsageRecord,
	opened openedAISettlement) ([]aiBudgetCrossing, error) {
	plan := aiSettlementPlan{
		endUser:  record.EndUser != "",
		tags:     len(record.Tags) > 0,
		counters: record.Usage.BudgetTokens() > 0 || record.CostMillicents > 0,
		binding:  record.Usage.BudgetTokens() > 0 && appCaller(record.Caller),
		lease:    leaseID != uuid.Nil,
		content:  record.Log != nil && record.Log.Content != nil,
		route:    record.Route != nil,
	}
	batch := &pgx.Batch{}
	queueAIUsageRollup(batch, integrationID, record)
	if plan.endUser {
		queueAIUserUsageRollup(batch, integrationID, record)
	}
	if plan.tags {
		queueAITagUsageRollup(batch, integrationID, record)
	}
	queueAIUsageCounters(batch, integrationID, record, plan)
	if plan.lease {
		batch.Queue(`DELETE FROM outbound_admission_leases WHERE integration_id = $1 AND lease_id = $2`, integrationID, leaseID)
	}
	if plan.content {
		queueAIRequestContent(batch, integrationID, opened.logID, record.Log.Content)
	}
	if plan.route {
		queueAIRouteAttempt(batch, record)
	}
	results := tx.SendBatch(ctx, batch)
	crossings, err := scanAIUsageRows(results, record, opened, plan)
	if closeErr := results.Close(); err == nil {
		err = closeErr
	}
	return crossings, err
}

// aiSettlementPlan names the statements one settlement sends.
type aiSettlementPlan struct {
	endUser, tags, counters, binding, lease, content, route bool
}

// queueAIRouteAttempt adds a route attempt to its target's row for the
// current UTC minute while the route's canary is active, or when its
// strategy judges targets by these rows (ADR-1434). Otherwise, or for a
// route deleted meanwhile, it writes nothing: the table has no foreign key,
// so a settlement never fails on it.
func queueAIRouteAttempt(batch *pgx.Batch, record AIUsageRecord) {
	attempt := record.Route
	var errorCount, ttfbCalls, ttfbMS int64
	if attempt.Error {
		errorCount = 1
	}
	if attempt.TTFBMS != nil {
		ttfbCalls, ttfbMS = 1, *attempt.TTFBMS
	}
	batch.Queue(`
		INSERT INTO outbound_ai_route_target_minutes AS stats
		    (route_id, position, minute, calls, errors, ttfb_calls, ttfb_ms_total, cost_millicents)
		SELECT $1, $2, date_trunc('minute', now()), 1, $3, $4, $5, $6
		 WHERE EXISTS (SELECT 1 FROM outbound_ai_route_canaries canary WHERE canary.route_id = $1 AND canary.state = 'active')
		    OR EXISTS (SELECT 1 FROM outbound_ai_routes route WHERE route.id = $1 AND route.strategy IN ('latency', 'cost'))
		ON CONFLICT (route_id, position, minute) DO UPDATE SET
		    calls = stats.calls + 1,
		    errors = stats.errors + EXCLUDED.errors,
		    ttfb_calls = stats.ttfb_calls + EXCLUDED.ttfb_calls,
		    ttfb_ms_total = stats.ttfb_ms_total + EXCLUDED.ttfb_ms_total,
		    cost_millicents = stats.cost_millicents + EXCLUDED.cost_millicents`,
		uuid.MustParse(attempt.RouteID), attempt.Position, errorCount, ttfbCalls, ttfbMS, record.CostMillicents)
}

// queueAIUsageCounters adds the call to the admission and binding counters.
// The counters follow admission's UTC-day and calendar-month resets; a call
// that straddles midnight counts toward the day it settles in. A call with
// nothing to count still locks the state row when it deletes its lease, so
// the lease is deleted under the same lock order as admission.
func queueAIUsageCounters(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord, plan aiSettlementPlan) {
	if !plan.counters {
		if plan.lease {
			batch.Queue(`SELECT 1 FROM outbound_admission_state WHERE integration_id = $1 FOR UPDATE`, integrationID)
		}
	} else {
		batch.Queue(`
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
			integrationID, record.Usage.BudgetTokens(), record.CostMillicents)
	}
	// ADR-990: the app's own binding counter, when the app has a binding row.
	// Operator attachments and Runs have none, so nothing is updated.
	if plan.binding {
		batch.Queue(`
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
			integrationID, uuid.MustParse(record.Caller), record.Usage.BudgetTokens())
	}
}

// scanAIUsageRows reads the settlement's results in the order they were
// queued and turns the counters into budget crossings.
func scanAIUsageRows(results pgx.BatchResults, record AIUsageRecord, opened openedAISettlement, plan aiSettlementPlan) ([]aiBudgetCrossing, error) {
	if _, err := results.Exec(); err != nil {
		return nil, err
	}
	var user aiUserDay
	if plan.endUser {
		if err := results.QueryRow().Scan(&user.endUser, &user.tokens, &user.cost); err != nil {
			return nil, err
		}
	}
	var tagDays []aiTagDay
	if plan.tags {
		var err error
		if tagDays, err = scanAITagDays(results); err != nil {
			return nil, err
		}
	}
	budgetTokens, cost := record.Usage.BudgetTokens(), record.CostMillicents
	limits := opened.limits
	day, month := utcDayStart(opened.now), utcMonthStart(opened.now)
	var crossings []aiBudgetCrossing
	if !plan.counters && plan.lease {
		var locked int
		if err := results.QueryRow().Scan(&locked); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	} else if plan.counters {
		var dailyTokens, dailyCost, monthlyCost int64
		err := results.QueryRow().Scan(&dailyTokens, &dailyCost, &monthlyCost)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			crossings = append(crossings,
				aiBudgetCrossing{api.OutboundAIBudgetDailyTokens, "", day, positiveLimit(limits.dailyTokens), dailyTokens - budgetTokens, dailyTokens, "", ""},
				aiBudgetCrossing{api.OutboundAIBudgetDailyCost, "", day, positiveLimit(limits.dailyCost), dailyCost - cost, dailyCost, "", ""},
				aiBudgetCrossing{api.OutboundAIBudgetMonthlyCost, "", month, positiveLimit(limits.monthlyCost), monthlyCost - cost, monthlyCost, "", ""})
		}
	}
	if plan.binding {
		var bindingTokens, bindingLimit int64
		err := results.QueryRow().Scan(&bindingTokens, &bindingLimit)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			crossings = append(crossings, aiBudgetCrossing{api.OutboundAIBudgetAppDailyTokens, record.Caller, day,
				positiveLimit(bindingLimit), bindingTokens - budgetTokens, bindingTokens, "", ""})
		}
	}
	if plan.lease {
		if _, err := results.Exec(); err != nil {
			return nil, err
		}
	}
	if plan.content {
		for range 2 {
			if _, err := results.Exec(); err != nil {
				return nil, err
			}
		}
	}
	if plan.route {
		if _, err := results.Exec(); err != nil {
			return nil, err
		}
	}
	crossings = append(crossings, userBudgetCrossings(user.endUser, day, positiveLimit(limits.userTokens),
		positiveLimit(limits.userCost), user.tokens, user.cost, record.Usage.InputTokens+record.Usage.OutputTokens, cost)...)
	crossings = append(crossings, tagBudgetCrossings(tagDays, day, cost)...)
	return crossings, nil
}

// cachedRequests counts a cache hit (ADR-1440).
func cachedRequests(record AIUsageRecord) int64 {
	if record.Cached {
		return 1
	}
	return 0
}

func queueAIUsageRollup(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord) {
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
	batch.Queue(`
		INSERT INTO outbound_ai_usage_daily AS usage
		    (integration_id, usage_date, caller, model, requests, error_requests, usage_unknown_requests,
		     input_tokens, cached_input_tokens, cache_write_tokens, output_tokens, cost_millicents,
		     images, audio_seconds, characters, video_seconds, web_searches, file_searches, code_session_gb,
		     cached_requests, cache_saved_millicents, prompt_cache_saved_millicents)
		SELECT $1, day.today, $2,
		       CASE WHEN EXISTS (SELECT 1 FROM outbound_ai_usage_daily existing
		                          WHERE existing.integration_id = $1 AND existing.usage_date = day.today
		                            AND existing.model = $3)
		              OR (SELECT count(DISTINCT existing.model) FROM outbound_ai_usage_daily existing
		                   WHERE existing.integration_id = $1 AND existing.usage_date = day.today) < $4
		            THEN $3 ELSE $5 END,
		       1, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
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
		    images = usage.images + EXCLUDED.images,
		    audio_seconds = usage.audio_seconds + EXCLUDED.audio_seconds,
		    characters = usage.characters + EXCLUDED.characters,
		    video_seconds = usage.video_seconds + EXCLUDED.video_seconds,
		    web_searches = usage.web_searches + EXCLUDED.web_searches,
		    file_searches = usage.file_searches + EXCLUDED.file_searches,
		    code_session_gb = usage.code_session_gb + EXCLUDED.code_session_gb,
		    cached_requests = usage.cached_requests + EXCLUDED.cached_requests,
		    cache_saved_millicents = usage.cache_saved_millicents + EXCLUDED.cache_saved_millicents,
		    prompt_cache_saved_millicents = usage.prompt_cache_saved_millicents + EXCLUDED.prompt_cache_saved_millicents,
		    updated_at = now()`,
		integrationID, record.Caller, usage.Model, api.OutboundAIUsageModelsPerDay, aiUsageOverflowModel,
		errorRequests, unknownRequests, usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteTokens,
		usage.OutputTokens, record.CostMillicents, usage.Images, usage.AudioSeconds, usage.Characters, usage.VideoSeconds,
		usage.WebSearches, usage.FileSearches, usage.CodeSessionGB, cachedRequests(record), record.CacheSavedMillicents,
		record.PromptCacheSavedMillicents)
}

// recordAIBudgetAlerts stores first-time alerts and enqueues one
// outbound.ai_budget.threshold webhook per affected app: the app itself for
// an app budget, every bound app for an integration budget, and the calling
// app for an end user's budget (ADR-995).
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
			    (integration_id, account_id, budget, app_id, end_user, period_start, threshold_percent, limit_value, used_value, tag)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8, $9, NULLIF($10, ''))
			ON CONFLICT DO NOTHING
			RETURNING id, crossed_at`, integrationID, limits.accountID, alert.Budget, appID, alert.EndUser, alert.PeriodStart,
			alert.ThresholdPercent, alert.Limit, alert.Used, alert.Tag).Scan(&id, &crossedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		alert.ID, alert.CrossedAt = id.String(), crossedAt.UTC()
		notify := alert.AppID
		perCall := alert.EndUser != "" || alert.Tag != ""
		if perCall {
			// A user's or tag's budget notifies the app whose call crossed it;
			// Runs have no app to notify, and the alert is still listed.
			notify = ""
			if appCaller(caller) {
				notify = caller
			}
		}
		if !perCall || notify != "" {
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
	if alert.Tag != "" {
		payload.Tag = &alert.Tag
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

func queueAIRequestLog(batch *pgx.Batch, integrationID uuid.UUID, record AIUsageRecord) {
	var routeID *uuid.UUID
	if record.Log.RouteID != "" {
		parsed := uuid.MustParse(record.Log.RouteID)
		routeID = &parsed
	}
	usage := record.Usage
	batch.Queue(`
		INSERT INTO outbound_ai_request_log
		    (integration_id, caller, route_id, model, status, outcome, input_tokens, cached_input_tokens,
		     cache_write_tokens, output_tokens, cost_millicents, duration_ms, ttfb_ms, attempts, end_user, tags)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, NULLIF($15, ''), $16)
		RETURNING id`,
		integrationID, record.Caller, routeID, usage.Model, record.Log.Status, string(record.Outcome), usage.InputTokens,
		usage.CachedInputTokens, usage.CacheWriteTokens, usage.OutputTokens, record.CostMillicents,
		record.Log.DurationMS, record.Log.TTFBMS, record.Log.Attempts, record.EndUser, nilIfEmpty(record.Tags))
}

// queueAIRequestContent stores a logged call's captured bodies while the
// integration has fewer than the day's capped captures (ADR-996). A
// transaction lock per integration keeps concurrent settlements from
// overshooting the cap; it is taken after the admission-state row, so the
// lock is never held while waiting on that row.
func queueAIRequestContent(batch *pgx.Batch, integrationID, logID uuid.UUID, content *AIRequestContent) {
	batch.Queue(`SELECT pg_advisory_xact_lock(hashtextextended('outbound-ai-content:' || $1::uuid::text, 0))`, integrationID)
	batch.Queue(`
		INSERT INTO outbound_ai_request_content
		    (log_id, integration_id, request_body, request_truncated, response_body, response_truncated, response_content_type,
		     request_path)
		SELECT $1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($9, '')
		 WHERE (SELECT count(*) FROM (SELECT 1 FROM outbound_ai_request_content
		                               WHERE integration_id = $2
		                                 AND created_at >= date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC'
		                               LIMIT $8) captured) < $8`,
		logID, integrationID, nonNilBytes(content.Request), content.RequestTruncated, nonNilBytes(content.Response),
		content.ResponseTruncated, content.ResponseContentType, api.OutboundAIContentCapturesPerDay, content.RequestPath)
}

func nonNilBytes(b []byte) []byte {
	if b == nil {
		return []byte{}
	}
	return b
}

// PruneAIUsage deletes UTC-day AI usage rollups and budget alerts older
// than the rollup retention, and request-log rows and captured content
// older than theirs. It is
// idempotent, so every outboundd replica may run it.
func (b *PostgresBackend) PruneAIUsage(ctx context.Context) (int64, error) {
	var total int64
	for _, statement := range []struct {
		sql string
		// age is the retention, in the unit the statement names.
		age int
	}{
		{`DELETE FROM outbound_ai_usage_daily WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUsageRetentionDays},
		{`DELETE FROM outbound_ai_budget_alerts WHERE period_start < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUsageRetentionDays},
		{`DELETE FROM outbound_ai_request_log WHERE created_at < now() - make_interval(days => $1::int)`, api.OutboundAIRequestLogRetentionDays},
		// ADR-996: captured content expires sooner, and an integration that
		// turned capture off loses what it captured within one prune.
		{`DELETE FROM outbound_ai_request_content content
		   WHERE content.created_at < now() - make_interval(days => $1::int)
		      OR NOT EXISTS (SELECT 1 FROM outbound_integrations integration
		                      WHERE integration.id = content.integration_id AND integration.ai_request_log_content)`,
			api.OutboundAIRequestContentRetentionDays},
		// ADR-997: replays and their results.
		{`DELETE FROM outbound_ai_replays WHERE created_at < now() - make_interval(days => $1::int)`, api.OutboundAIReplayRetentionDays},
		{`DELETE FROM outbound_ai_user_usage_daily WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUserUsageRetentionDays},
		// ADR-1436: tag rows follow the per-user rollup retention.
		{`DELETE FROM outbound_ai_tag_usage_daily WHERE usage_date < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUserUsageRetentionDays},
		// ADR-995: end users' alerts follow the per-user rollup retention, and
		// tags' alerts the tag rollup's (ADR-1438).
		{`DELETE FROM outbound_ai_budget_alerts WHERE (end_user IS NOT NULL OR tag IS NOT NULL) AND period_start < (now() AT TIME ZONE 'UTC')::date - $1::int`, api.OutboundAIUserUsageRetentionDays},
		// ADR-1120: a settled call's key only has to outlive its retries.
		{`DELETE FROM outbound_ai_settlements WHERE settled_at < now() - make_interval(mins => $1::int)`, api.OutboundAISettlementKeyRetentionMinutes},
		// ADR-1340: canary rows and export lines.
		{`DELETE FROM outbound_ai_route_target_minutes WHERE minute < now() - make_interval(hours => $1::int)`, api.OutboundAIRouteStatsRetentionHours},
		{`DELETE FROM outbound_ai_log_exports WHERE created_at < now() - make_interval(hours => $1::int)`, api.OutboundAILogExportRetentionHours},
		// ADR-1431: settled batches, evaluations with their items, and
		// tokens that expired or were revoked.
		{`DELETE FROM outbound_ai_batches WHERE state = 'settled' AND settled_at < now() - make_interval(days => $1::int)`, api.OutboundAIBatchRetentionDays},
		{`DELETE FROM outbound_ai_evaluations WHERE created_at < now() - make_interval(days => $1::int)`, api.OutboundAIEvaluationRetentionDays},
		{`DELETE FROM outbound_ai_access_tokens
		   WHERE least(expires_at, COALESCE(revoked_at, expires_at)) < now() - make_interval(days => $1::int)`,
			api.OutboundAIAccessTokenRetentionDays},
	} {
		command, err := b.pool.Exec(ctx, statement.sql, statement.age)
		if err != nil {
			return total, err
		}
		total += command.RowsAffected()
	}
	return total, nil
}

var _ AIUsageBackend = (*PostgresBackend)(nil)
var _ AIBudgetAlertRecorder = (*PostgresBackend)(nil)
