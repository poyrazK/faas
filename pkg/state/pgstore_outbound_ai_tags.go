package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAITagStore = (*PgStore)(nil)

// GetOutboundAITagUsage reads one UTC day of per-tag rollups (ADR-1410),
// ordered by cost, then tokens, then tag, with each tag's limit.
func (s *PgStore) GetOutboundAITagUsage(ctx context.Context, accountID, integrationID, usageDate, key string, limit int) (OutboundAITagUsage, error) {
	usageDate, ok := outboundAITagUsageQuery(usageDate, key, time.Now())
	if !ok {
		return OutboundAITagUsage{}, ErrInvalidArgument
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return OutboundAITagUsage{}, err
	}
	usage := OutboundAITagUsage{UsageDate: usageDate, Items: make([]OutboundAITagUsageItem, 0)}
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM outbound_ai_tag_usage_daily WHERE integration_id = $1 AND usage_date = $2::date`,
		mustPgUUID(integrationID), usageDate).Scan(&usage.TrackedTags); err != nil {
		return OutboundAITagUsage{}, mapErr(err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT usage.tag, usage.requests, usage.error_requests, usage.input_tokens, usage.output_tokens, usage.cost_millicents,
		       tag_limit.daily_cost_limit_millicents
		  FROM outbound_ai_tag_usage_daily usage
		  LEFT JOIN outbound_ai_tag_limits tag_limit ON tag_limit.integration_id = usage.integration_id AND tag_limit.tag = usage.tag
		 WHERE usage.integration_id = $1 AND usage.usage_date = $2::date AND ($3 = '' OR usage.tag LIKE $3 || '=%')
		 ORDER BY usage.cost_millicents DESC, usage.input_tokens + usage.output_tokens DESC, usage.tag
		 LIMIT $4`, mustPgUUID(integrationID), usageDate, key, boundedPage(limit, api.OutboundAITagUsagePageMax))
	if err != nil {
		return OutboundAITagUsage{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var item OutboundAITagUsageItem
		if err := rows.Scan(&item.Tag, &item.Requests, &item.ErrorRequests, &item.InputTokens, &item.OutputTokens,
			&item.CostMillicents, &item.DailyCostLimit); err != nil {
			return OutboundAITagUsage{}, mapErr(err)
		}
		usage.Items = append(usage.Items, item)
	}
	return usage, mapErr(rows.Err())
}

func (s *PgStore) ListOutboundAITagLimits(ctx context.Context, accountID, integrationID string) ([]api.OutboundAITagLimit, error) {
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT tag, daily_cost_limit_millicents FROM outbound_ai_tag_limits WHERE integration_id = $1 ORDER BY tag`,
		mustPgUUID(integrationID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	limits := make([]api.OutboundAITagLimit, 0)
	for rows.Next() {
		var limit api.OutboundAITagLimit
		if err := rows.Scan(&limit.Tag, &limit.DailyCostLimitMillicents); err != nil {
			return nil, mapErr(err)
		}
		limits = append(limits, limit)
	}
	return limits, mapErr(rows.Err())
}

// SetOutboundAITagLimits replaces the limits in one transaction. The
// integration must be the account's managed AI integration; bumping its
// updated_at gives outboundd a new policy revision.
func (s *PgStore) SetOutboundAITagLimits(ctx context.Context, accountID, integrationID string, limits []api.OutboundAITagLimit) error {
	if !api.ValidOutboundAITagLimits(limits) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var hasProvider bool
	err = tx.QueryRow(ctx, `
		UPDATE outbound_integrations SET updated_at = now()
		 WHERE account_id = $1 AND id = $2 AND provider_auth_mode = 'managed'
		RETURNING ai_provider IS NOT NULL`, mustPgUUID(accountID), mustPgUUID(integrationID)).Scan(&hasProvider)
	if err != nil {
		return mapErr(err)
	}
	if !hasProvider && len(limits) > 0 {
		return ErrInvalidArgument
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_tag_limits WHERE integration_id = $1`, mustPgUUID(integrationID)); err != nil {
		return mapErr(err)
	}
	for _, limit := range limits {
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbound_ai_tag_limits (integration_id, tag, daily_cost_limit_millicents) VALUES ($1, $2, $3)`,
			mustPgUUID(integrationID), limit.Tag, limit.DailyCostLimitMillicents); err != nil {
			return mapErr(err)
		}
	}
	return mapErr(tx.Commit(ctx))
}
