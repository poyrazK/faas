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

// outboundAIUserExportBatch is the keyset page size of a per-user export.
const outboundAIUserExportBatch = 2000

// ExportOutboundAIUserUsage pages the range by (usage_date, end_user), so
// a whole retention window of capped rows streams without being held.
func (s *PgStore) ExportOutboundAIUserUsage(ctx context.Context, accountID, integrationID, from, to string,
	visit func(OutboundAIUserUsageRow) error) error {
	from, to, ok := outboundAIUserExportRange(from, to, time.Now())
	if !ok {
		return ErrInvalidArgument
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return err
	}
	afterDate, afterUser := from, ""
	for {
		rows, err := s.pool.Query(ctx, `
			SELECT usage_date::text, end_user, requests, error_requests, input_tokens, output_tokens, cost_millicents
			  FROM outbound_ai_user_usage_daily
			 WHERE integration_id = $1 AND (usage_date, end_user) > ($2::date, $3) AND usage_date <= $4::date
			 ORDER BY usage_date, end_user
			 LIMIT $5`, mustPgUUID(integrationID), afterDate, afterUser, to, outboundAIUserExportBatch)
		if err != nil {
			return mapErr(err)
		}
		batch := make([]OutboundAIUserUsageRow, 0, outboundAIUserExportBatch)
		for rows.Next() {
			var row OutboundAIUserUsageRow
			if err := rows.Scan(&row.UsageDate, &row.EndUser, &row.Requests, &row.ErrorRequests, &row.InputTokens,
				&row.OutputTokens, &row.CostMillicents); err != nil {
				rows.Close()
				return mapErr(err)
			}
			batch = append(batch, row)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return mapErr(err)
		}
		for _, row := range batch {
			if err := visit(row); err != nil {
				return err
			}
		}
		if len(batch) < outboundAIUserExportBatch {
			return nil
		}
		last := batch[len(batch)-1]
		afterDate, afterUser = last.UsageDate, last.EndUser
	}
}

// GetOutboundAIUserUsage reads one UTC day of per-end-user rollups (ADR-994),
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
