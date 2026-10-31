package state

import (
	"context"
	"time"

	"github.com/google/uuid"
)

var _ OutboundAIDeploymentStatsStore = (*PgStore)(nil)

// ListOutboundAIDeploymentStats sums the app's minute rows by deployment
// through the (app, minute) index.
func (s *PgStore) ListOutboundAIDeploymentStats(ctx context.Context, appID string, since time.Time, limit int) ([]OutboundAIDeploymentStats, error) {
	if _, err := uuid.Parse(appID); err != nil {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT deployment_id::text, min(minute), max(minute), sum(calls)::bigint, sum(error_calls)::bigint,
		       sum(flagged_calls)::bigint, sum(cost_millicents)::bigint, sum(input_tokens)::bigint,
		       sum(output_tokens)::bigint, sum(ttfb_calls)::bigint, sum(ttfb_ms_total)::bigint
		  FROM outbound_ai_deployment_minutes
		 WHERE app_id = $1 AND minute >= date_trunc('minute', $2::timestamptz)
		 GROUP BY deployment_id
		 ORDER BY max(minute) DESC, deployment_id
		 LIMIT $3`, mustPgUUID(appID), since.UTC(), max(limit, 1))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIDeploymentStats, 0)
	for rows.Next() {
		var item OutboundAIDeploymentStats
		if err := rows.Scan(&item.DeploymentID, &item.FirstMinute, &item.LastMinute, &item.Calls, &item.ErrorCalls,
			&item.FlaggedCalls, &item.CostMillicents, &item.InputTokens, &item.OutputTokens, &item.TTFBCalls,
			&item.TTFBMSTotal); err != nil {
			return nil, mapErr(err)
		}
		item.FirstMinute, item.LastMinute = item.FirstMinute.UTC(), item.LastMinute.UTC()
		out = append(out, item)
	}
	return out, mapErr(rows.Err())
}
