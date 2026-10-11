package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIStore = (*PgStore)(nil)

// outboundAIUsageItemsMax bounds one day's rollup read. outboundd caps
// distinct models per day, and callers are bound apps plus "runs".
const outboundAIUsageItemsMax = 5000

// SetOutboundAIPolicy replaces a customer integration's AI gateway settings
// (ADR-979). Bumping updated_at gives outboundd a new policy revision.
func (s *PgStore) SetOutboundAIPolicy(ctx context.Context, accountID, integrationID, provider string, dailyTokenLimit *int64) error {
	if !api.ValidOutboundAIPolicy(provider, dailyTokenLimit) {
		return ErrInvalidArgument
	}
	command, err := s.pool.Exec(ctx, `
		UPDATE outbound_integrations
		   SET ai_provider = NULLIF($3, ''), ai_daily_token_limit = $4, updated_at = now()
		 WHERE account_id = $1 AND id = $2 AND owner_kind = 'customer'
		   AND provider_auth_mode = 'managed' AND enabled`,
		mustPgUUID(accountID), mustPgUUID(integrationID), provider, dailyTokenLimit)
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}

// GetOutboundAIUsage reads one UTC day of the rollups outboundd writes.
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
	usage := OutboundAIUsage{UsageDate: usageDate, ResetsAt: day.AddDate(0, 0, 1), Items: make([]OutboundAIUsageItem, 0)}
	var dailyTokenLimit pgtype.Int8
	var liveTokenCount int64
	var isToday bool
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(integration.ai_provider, ''), integration.ai_daily_token_limit,
		       CASE WHEN admission.daily_usage_date = $3::date THEN admission.daily_ai_token_count ELSE 0 END,
		       (now() AT TIME ZONE 'UTC')::date = $3::date
		  FROM outbound_integrations integration
		  LEFT JOIN outbound_admission_state admission ON admission.integration_id = integration.id
		 WHERE integration.account_id = $1 AND integration.id = $2
		   AND integration.provider_auth_mode = 'managed'`,
		mustPgUUID(accountID), mustPgUUID(integrationID), usageDate).
		Scan(&usage.AIProvider, &dailyTokenLimit, &liveTokenCount, &isToday)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIUsage{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIUsage{}, mapErr(err)
	}
	usage.DailyTokenLimit = outboundAITokenLimit(dailyTokenLimit)
	rows, err := s.pool.Query(ctx, `
		SELECT caller, model, requests, error_requests, usage_unknown_requests,
		       input_tokens, cached_input_tokens, cache_write_tokens, output_tokens
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
			&item.InputTokens, &item.CachedInputTokens, &item.CacheWriteTokens, &item.OutputTokens); err != nil {
			return OutboundAIUsage{}, mapErr(err)
		}
		rollupTokens += item.InputTokens + item.OutputTokens
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
