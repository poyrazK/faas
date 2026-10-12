package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIUserStore = (*PgStore)(nil)

// GetOutboundAIUserUsage reads one UTC day of per-end-user rollups (ADR-992),
// ordered by cost, then tokens, then end user.
func (s *PgStore) GetOutboundAIUserUsage(ctx context.Context, accountID, integrationID, usageDate, endUser string, limit int) (OutboundAIUserUsage, error) {
	usageDate, ok := outboundAIUserUsageQuery(usageDate, endUser, time.Now())
	if !ok {
		return OutboundAIUserUsage{}, ErrInvalidArgument
	}
	usage := OutboundAIUserUsage{UsageDate: usageDate, Items: make([]OutboundAIUserUsageItem, 0)}
	var tokenLimit, costLimit pgtype.Int8
	err := s.pool.QueryRow(ctx, `
		SELECT integration.ai_user_daily_token_limit, integration.ai_user_daily_cost_limit_millicents,
		       (SELECT count(*) FROM outbound_ai_user_usage_daily rollup
		         WHERE rollup.integration_id = integration.id AND rollup.usage_date = $3::date)
		  FROM outbound_integrations integration
		 WHERE integration.account_id = $1 AND integration.id = $2
		   AND integration.provider_auth_mode = 'managed'`,
		mustPgUUID(accountID), mustPgUUID(integrationID), usageDate).Scan(&tokenLimit, &costLimit, &usage.TrackedUsers)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIUserUsage{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIUserUsage{}, mapErr(err)
	}
	usage.UserDailyTokenLimit = outboundAITokenLimit(tokenLimit)
	usage.UserDailyCostLimit = outboundAITokenLimit(costLimit)
	rows, err := s.pool.Query(ctx, `
		SELECT end_user, requests, error_requests, input_tokens, output_tokens, cost_millicents
		  FROM outbound_ai_user_usage_daily
		 WHERE integration_id = $1 AND usage_date = $2::date AND ($3 = '' OR end_user = $3)
		 ORDER BY cost_millicents DESC, input_tokens + output_tokens DESC, end_user
		 LIMIT $4`, mustPgUUID(integrationID), usageDate, endUser, boundedPage(limit, api.OutboundAIUserUsagePageMax))
	if err != nil {
		return OutboundAIUserUsage{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item OutboundAIUserUsageItem
		if err := rows.Scan(&item.EndUser, &item.Requests, &item.ErrorRequests, &item.InputTokens, &item.OutputTokens,
			&item.CostMillicents); err != nil {
			return OutboundAIUserUsage{}, mapErr(err)
		}
		usage.Items = append(usage.Items, item)
	}
	return usage, mapErr(rows.Err())
}
