package outbound

import (
	"context"

	"github.com/google/uuid"
)

// ActiveAIRouteCanaries reads every active canary with its first target's
// and its own counts over its window: from the later of its start and the
// window's start, by whole minutes (ADR-1340).
func (b *PostgresBackend) ActiveAIRouteCanaries(ctx context.Context) ([]AIRouteCanaryJudgement, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT canary.route_id::text, canary.position, canary.max_error_rate_increase,
		       canary.max_latency_increase_percent, canary.min_calls,
		       COALESCE(sum(stats.calls) FILTER (WHERE stats.position = 1), 0)::bigint,
		       COALESCE(sum(stats.errors) FILTER (WHERE stats.position = 1), 0)::bigint,
		       COALESCE(sum(stats.ttfb_calls) FILTER (WHERE stats.position = 1), 0)::bigint,
		       COALESCE(sum(stats.ttfb_ms_total) FILTER (WHERE stats.position = 1), 0)::bigint,
		       COALESCE(sum(stats.calls) FILTER (WHERE stats.position = canary.position), 0)::bigint,
		       COALESCE(sum(stats.errors) FILTER (WHERE stats.position = canary.position), 0)::bigint,
		       COALESCE(sum(stats.ttfb_calls) FILTER (WHERE stats.position = canary.position), 0)::bigint,
		       COALESCE(sum(stats.ttfb_ms_total) FILTER (WHERE stats.position = canary.position), 0)::bigint
		  FROM outbound_ai_route_canaries canary
		  LEFT JOIN outbound_ai_route_target_minutes stats
		    ON stats.route_id = canary.route_id AND stats.position IN (1, canary.position)
		   AND stats.minute >= date_trunc('minute', greatest(canary.started_at,
		                                                      now() - make_interval(mins => canary.window_minutes::int)))
		 WHERE canary.state = 'active'
		 GROUP BY canary.route_id, canary.position, canary.max_error_rate_increase,
		          canary.max_latency_increase_percent, canary.min_calls`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var judgements []AIRouteCanaryJudgement
	for rows.Next() {
		var j AIRouteCanaryJudgement
		if err := rows.Scan(&j.RouteID, &j.Position, &j.MaxErrorRateIncrease, &j.MaxLatencyIncreasePercent, &j.MinCalls,
			&j.Baseline.Calls, &j.Baseline.Errors, &j.Baseline.TTFBCalls, &j.Baseline.TTFBMSTotal,
			&j.Canary.Calls, &j.Canary.Errors, &j.Canary.TTFBCalls, &j.Canary.TTFBMSTotal); err != nil {
			return nil, err
		}
		judgements = append(judgements, j)
	}
	return judgements, rows.Err()
}

// RollBackAIRouteCanary rolls back a canary that is still active at the
// judged position, so a canary apid replaced meanwhile is left alone.
func (b *PostgresBackend) RollBackAIRouteCanary(ctx context.Context, routeID string, position int, reason string) (bool, error) {
	id, err := uuid.Parse(routeID)
	if err != nil {
		return false, err
	}
	command, err := b.pool.Exec(ctx, `
		UPDATE outbound_ai_route_canaries SET state = 'rolled_back', reason = $3, decided_at = now()
		 WHERE route_id = $1 AND position = $2 AND state = 'active'`, id, position, reason)
	if err != nil {
		return false, err
	}
	return command.RowsAffected() == 1, nil
}

var _ AIRouteCanaryStore = (*PostgresBackend)(nil)
