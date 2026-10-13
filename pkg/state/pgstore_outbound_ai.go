package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIStore = (*PgStore)(nil)

// outboundAIUsageItemsMax bounds one day's rollup read. outboundd caps
// distinct models per day, and callers are bound apps plus "runs".
const outboundAIUsageItemsMax = 5000

// SetOutboundAIPolicy replaces a customer integration's AI gateway settings
// (ADR-988, ADR-990). Bumping updated_at gives outboundd a new policy
// revision.
func (s *PgStore) SetOutboundAIPolicy(ctx context.Context, accountID, integrationID string, policy OutboundAIPolicy) error {
	if !policy.valid() {
		return ErrInvalidArgument
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE outbound_integrations
		   SET ai_provider = NULLIF($3, ''), ai_daily_token_limit = $4,
		       ai_daily_cost_limit_millicents = $5, ai_monthly_cost_limit_millicents = $6,
		       ai_request_log_enabled = $7, ai_user_daily_token_limit = $8,
		       ai_user_daily_cost_limit_millicents = $9, ai_request_log_content = $10, updated_at = now()
		 WHERE account_id = $1 AND id = $2 AND owner_kind = 'customer'
		   AND provider_auth_mode = 'managed' AND enabled`,
		mustPgUUID(accountID), mustPgUUID(integrationID), policy.Provider, policy.DailyTokenLimit,
		policy.DailyCostLimit, policy.MonthlyCostLimit, policy.RequestLogEnabled,
		policy.UserDailyTokenLimit, policy.UserDailyCostLimit, policy.RequestLogContent)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// GetOutboundAIUsage reads one UTC day of the rollups outboundd writes, plus
// the settled cost of that day's calendar month through the day.
// usageDate is YYYY-MM-DD inside the retention window; empty means today.
func (s *PgStore) GetOutboundAIUsage(ctx context.Context, accountID, integrationID, usageDate string) (OutboundAIUsage, error) {
	now := time.Now().UTC()
	if usageDate == "" {
		usageDate = now.Format("2006-01-02")
	}
	day, ok := validOutboundAIUsageDate(usageDate, now)
	if !ok {
		return OutboundAIUsage{}, ErrInvalidArgument
	}
	usage := OutboundAIUsage{UsageDate: usageDate, Month: day.Format("2006-01"), ResetsAt: day.AddDate(0, 0, 1), Items: make([]OutboundAIUsageItem, 0)}
	var dailyTokenLimit, dailyCostLimit, monthlyCostLimit pgtype.Int8
	var liveTokenCount int64
	var isToday bool
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(integration.ai_provider, ''), integration.ai_daily_token_limit,
		       integration.ai_daily_cost_limit_millicents, integration.ai_monthly_cost_limit_millicents,
		       CASE WHEN admission.daily_usage_date = $3::date THEN admission.daily_ai_token_count ELSE 0 END,
		       (now() AT TIME ZONE 'UTC')::date = $3::date,
		       COALESCE((SELECT sum(rollup.cost_millicents) FROM outbound_ai_usage_daily rollup
		                  WHERE rollup.integration_id = integration.id
		                    AND rollup.usage_date BETWEEN date_trunc('month', $3::date)::date AND $3::date), 0)
		  FROM outbound_integrations integration
		  LEFT JOIN outbound_admission_state admission ON admission.integration_id = integration.id
		 WHERE integration.account_id = $1 AND integration.id = $2
		   AND integration.provider_auth_mode = 'managed'`,
		mustPgUUID(accountID), mustPgUUID(integrationID), usageDate).
		Scan(&usage.AIProvider, &dailyTokenLimit, &dailyCostLimit, &monthlyCostLimit, &liveTokenCount, &isToday, &usage.MonthlyCostMillicents)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIUsage{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIUsage{}, mapErr(err)
	}
	usage.DailyTokenLimit = outboundAITokenLimit(dailyTokenLimit)
	usage.DailyCostLimit = outboundAITokenLimit(dailyCostLimit)
	usage.MonthlyCostLimit = outboundAITokenLimit(monthlyCostLimit)
	rows, err := s.pool.Query(ctx, `
		SELECT caller, model, requests, error_requests, usage_unknown_requests,
		       input_tokens, cached_input_tokens, cache_write_tokens, output_tokens, cost_millicents,
		       images, audio_seconds, characters
		  FROM outbound_ai_usage_daily
		 WHERE integration_id = $1 AND usage_date = $2::date
		 ORDER BY caller, model
		 LIMIT $3`, mustPgUUID(integrationID), usageDate, outboundAIUsageItemsMax)
	if err != nil {
		return OutboundAIUsage{}, mapErr(err)
	}
	defer rows.Close()
	var rollupTokens int64
	for rows.Next() {
		var item OutboundAIUsageItem
		if err := rows.Scan(&item.Caller, &item.Model, &item.Requests, &item.ErrorRequests, &item.UsageUnknownRequests,
			&item.InputTokens, &item.CachedInputTokens, &item.CacheWriteTokens, &item.OutputTokens, &item.CostMillicents,
			&item.Images, &item.AudioSeconds, &item.Characters); err != nil {
			return OutboundAIUsage{}, mapErr(err)
		}
		rollupTokens += item.InputTokens + item.OutputTokens
		usage.DailyCostMillicents += item.CostMillicents
		usage.Items = append(usage.Items, item)
	}
	if err := rows.Err(); err != nil {
		return OutboundAIUsage{}, mapErr(err)
	}
	usage.DailyTokenCount = rollupTokens
	if isToday {
		usage.DailyTokenCount = liveTokenCount
	}
	return usage, nil
}

// SetOutboundBindingAITokenLimit sets or clears one app's daily AI token
// limit on its binding (ADR-990).
func (s *PgStore) SetOutboundBindingAITokenLimit(ctx context.Context, accountID, appID, integrationID string, limit *int64) error {
	if limit != nil && (*limit < 1 || *limit > api.MaxOutboundAITokensPerDay) {
		return ErrInvalidArgument
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE outbound_app_bindings binding
		   SET ai_daily_token_limit = $4
		  FROM outbound_integrations integration
		 WHERE binding.account_id = $1 AND binding.app_id = $2 AND binding.integration_id = $3
		   AND integration.id = binding.integration_id AND integration.account_id = $1
		   AND integration.ai_provider IS NOT NULL`,
		mustPgUUID(accountID), mustPgUUID(appID), mustPgUUID(integrationID), limit)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// GetOutboundAIPrices returns the price list currently in effect.
func (s *PgStore) GetOutboundAIPrices(ctx context.Context, accountID, integrationID string) (OutboundAIPriceList, error) {
	var exists bool
	if err := s.pool.QueryRow(ctx, `
		SELECT true FROM outbound_integrations
		 WHERE account_id = $1 AND id = $2 AND provider_auth_mode = 'managed'`,
		mustPgUUID(accountID), mustPgUUID(integrationID)).Scan(&exists); errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIPriceList{}, ErrNotFound
	} else if err != nil {
		return OutboundAIPriceList{}, mapErr(err)
	}
	list := OutboundAIPriceList{Entries: make([]api.OutboundAIPriceEntry, 0)}
	var listID pgtype.UUID
	var effectiveFrom time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id, effective_from FROM outbound_ai_price_lists
		 WHERE integration_id = $1 AND effective_from <= now()
		 ORDER BY effective_from DESC, created_at DESC
		 LIMIT 1`, mustPgUUID(integrationID)).Scan(&listID, &effectiveFrom)
	if errors.Is(err, pgx.ErrNoRows) {
		return list, nil
	}
	if err != nil {
		return OutboundAIPriceList{}, mapErr(err)
	}
	list.EffectiveFrom = &effectiveFrom
	rows, err := s.pool.Query(ctx, `
		SELECT model_prefix, input_millicents_per_mtok, cached_input_millicents_per_mtok,
		       cache_write_millicents_per_mtok, output_millicents_per_mtok,
		       input_image_millicents_per_mtok, output_image_millicents_per_mtok, millicents_per_image,
		       millicents_per_audio_minute, millicents_per_million_characters
		  FROM outbound_ai_price_entries WHERE price_list_id = $1
		 ORDER BY model_prefix`, listID)
	if err != nil {
		return OutboundAIPriceList{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var entry api.OutboundAIPriceEntry
		if err := rows.Scan(&entry.ModelPrefix, &entry.InputMillicentsPerMTok, &entry.CachedInputMillicentsPerMTok,
			&entry.CacheWriteMillicentsPerMTok, &entry.OutputMillicentsPerMTok, &entry.InputImageMillicentsPerMTok,
			&entry.OutputImageMillicentsPerMTok, &entry.MillicentsPerImage, &entry.MillicentsPerAudioMinute,
			&entry.MillicentsPerMillionCharacters); err != nil {
			return OutboundAIPriceList{}, mapErr(err)
		}
		list.Entries = append(list.Entries, entry)
	}
	return list, mapErr(rows.Err())
}

// SetOutboundAIPrices writes a new, immutable price list effective now.
// Earlier lists stay so past rollups keep the prices they were charged.
func (s *PgStore) SetOutboundAIPrices(ctx context.Context, accountID, integrationID string, entries []api.OutboundAIPriceEntry) error {
	if !api.ValidOutboundAIPriceEntries(entries) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id FROM outbound_integrations
		 WHERE account_id = $1 AND id = $2 AND owner_kind = 'customer' AND provider_auth_mode = 'managed'
		 FOR UPDATE`, mustPgUUID(accountID), mustPgUUID(integrationID)).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return mapErr(err)
	}
	listID := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO outbound_ai_price_lists (id, integration_id) VALUES ($1, $2)`,
		listID, mustPgUUID(integrationID)); err != nil {
		return mapErr(err)
	}
	for _, entry := range entries {
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbound_ai_price_entries
			    (price_list_id, model_prefix, input_millicents_per_mtok, cached_input_millicents_per_mtok,
			     cache_write_millicents_per_mtok, output_millicents_per_mtok, input_image_millicents_per_mtok,
			     output_image_millicents_per_mtok, millicents_per_image, millicents_per_audio_minute,
			     millicents_per_million_characters)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, listID, entry.ModelPrefix, entry.InputMillicentsPerMTok,
			entry.CachedInputMillicentsPerMTok, entry.CacheWriteMillicentsPerMTok, entry.OutputMillicentsPerMTok,
			entry.InputImageMillicentsPerMTok, entry.OutputImageMillicentsPerMTok, entry.MillicentsPerImage,
			entry.MillicentsPerAudioMinute, entry.MillicentsPerMillionCharacters); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(tx.Commit(ctx))
}
