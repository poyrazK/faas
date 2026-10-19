package outbound

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
)

// PostgresRouteResolver reads AI fallback routes that apid writes. Targets
// come back in position order, with the route's active canary (ADR-1340).
type PostgresRouteResolver struct{ pool *pgxpool.Pool }

func NewPostgresRouteResolver(pool *pgxpool.Pool) (*PostgresRouteResolver, error) {
	if pool == nil {
		return nil, errors.New("outbound route resolver: postgres pool is required")
	}
	return &PostgresRouteResolver{pool: pool}, nil
}

func (r *PostgresRouteResolver) Route(ctx context.Context, routeID string) (AIRoute, error) {
	id, err := uuid.Parse(routeID)
	if err != nil {
		return AIRoute{}, ErrRouteNotFound
	}
	// ADR-1434: a latency or cost route reads its targets' window counts in
	// the same query, so routing adds no round trip.
	rows, err := r.pool.Query(ctx, `
		SELECT route.account_id::text, route.format, route.strategy, route.max_ttfb_ms,
		       target.integration_id::text, COALESCE(target.model, ''), COALESCE(target.weight, 0),
		       canary.position, canary.percent,
		       stats.calls, stats.errors, stats.ttfb_calls, stats.ttfb_ms_total, stats.cost_millicents
		  FROM outbound_ai_routes route
		  JOIN outbound_ai_route_targets target ON target.route_id = route.id
		  LEFT JOIN outbound_ai_route_canaries canary ON canary.route_id = route.id AND canary.state = 'active'
		  LEFT JOIN LATERAL (
		       SELECT COALESCE(sum(minute.calls), 0)::bigint AS calls, COALESCE(sum(minute.errors), 0)::bigint AS errors,
		              COALESCE(sum(minute.ttfb_calls), 0)::bigint AS ttfb_calls,
		              COALESCE(sum(minute.ttfb_ms_total), 0)::bigint AS ttfb_ms_total,
		              COALESCE(sum(minute.cost_millicents), 0)::bigint AS cost_millicents
		         FROM outbound_ai_route_target_minutes minute
		        WHERE route.strategy IN ('latency', 'cost') AND minute.route_id = route.id AND minute.position = target.position
		          AND minute.minute >= date_trunc('minute', now() - make_interval(mins => $2::int))) stats ON true
		 WHERE route.id = $1
		 ORDER BY target.position`, id, api.OutboundAIRouteStatsWindowMinutes)
	if err != nil {
		return AIRoute{}, err
	}
	defer rows.Close()
	route := AIRoute{ID: routeID}
	for rows.Next() {
		var target AIRouteTarget
		var weight int
		var counts AIRouteTargetCounts
		var canaryPosition, canaryPercent *int
		if err := rows.Scan(&route.AccountID, &route.Format, &route.Strategy, &route.MaxTTFBMS, &target.IntegrationID, &target.Model,
			&weight, &canaryPosition, &canaryPercent, &counts.Calls, &counts.Errors, &counts.TTFBCalls, &counts.TTFBMSTotal,
			&counts.CostMillicents); err != nil {
			return AIRoute{}, err
		}
		route.Targets = append(route.Targets, target)
		route.Weights = append(route.Weights, weight)
		route.Stats = append(route.Stats, counts)
		if canaryPosition != nil && canaryPercent != nil {
			route.Canary = &AIRouteCanary{Position: *canaryPosition, Percent: *canaryPercent}
		}
	}
	if err := rows.Err(); err != nil {
		return AIRoute{}, err
	}
	if len(route.Targets) == 0 {
		return AIRoute{}, ErrRouteNotFound
	}
	return route, nil
}

var _ RouteResolver = (*PostgresRouteResolver)(nil)

// StaticRouteResolver serves fixed routes for tests and development.
type StaticRouteResolver map[string]AIRoute

func (s StaticRouteResolver) Route(_ context.Context, routeID string) (AIRoute, error) {
	route, ok := s[routeID]
	if !ok {
		return AIRoute{}, ErrRouteNotFound
	}
	route.Targets = append([]AIRouteTarget(nil), route.Targets...)
	if route.Canary != nil {
		canary := *route.Canary
		route.Canary = &canary
	}
	return route, nil
}
