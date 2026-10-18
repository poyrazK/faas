package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAITagStore = (*PgStore)(nil)

// GetOutboundAITagUsage reads one UTC day of per-tag rollups (ADR-1436),
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

// outboundAITagExportBatch is the keyset page size of a tag export.
const outboundAITagExportBatch = 2000

// ExportOutboundAITagUsage pages the range by (usage_date, tag), so a whole
// retention window of capped rows streams without being held (ADR-1438).
func (s *PgStore) ExportOutboundAITagUsage(ctx context.Context, accountID, integrationID, from, to, key string,
	visit func(OutboundAITagUsageRow) error) error {
	from, to, ok := outboundAITagExportRange(from, to, key, time.Now())
	if !ok {
		return ErrInvalidArgument
	}
	if err := s.outboundManagedIntegrationExists(ctx, accountID, integrationID); err != nil {
		return err
	}
	afterDate, afterTag := from, ""
	for {
		batch, err := s.outboundAITagExportPage(ctx, integrationID, afterDate, afterTag, to, key)
		if err != nil {
			return err
		}
		for _, row := range batch {
			if err := visit(row); err != nil {
				return err
			}
		}
		if len(batch) < outboundAITagExportBatch {
			return nil
		}
		last := batch[len(batch)-1]
		afterDate, afterTag = last.UsageDate, last.Tag
	}
}

func (s *PgStore) outboundAITagExportPage(ctx context.Context, integrationID, afterDate, afterTag, to, key string) ([]OutboundAITagUsageRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT usage_date::text, tag, requests, error_requests, input_tokens, output_tokens, cost_millicents
		  FROM outbound_ai_tag_usage_daily
		 WHERE integration_id = $1 AND (usage_date, tag) > ($2::date, $3) AND usage_date <= $4::date
		   AND ($5 = '' OR tag LIKE $5 || '=%')
		 ORDER BY usage_date, tag
		 LIMIT $6`, mustPgUUID(integrationID), afterDate, afterTag, to, key, outboundAITagExportBatch)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	batch := make([]OutboundAITagUsageRow, 0, outboundAITagExportBatch)
	for rows.Next() {
		var row OutboundAITagUsageRow
		if err := rows.Scan(&row.UsageDate, &row.Tag, &row.Requests, &row.ErrorRequests, &row.InputTokens,
			&row.OutputTokens, &row.CostMillicents); err != nil {
			return nil, mapErr(err)
		}
		batch = append(batch, row)
	}
	return batch, mapErr(rows.Err())
}
