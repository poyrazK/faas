package outbound

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
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
	rows, err := r.pool.Query(ctx, `
		SELECT route.account_id::text, route.format, target.integration_id::text, COALESCE(target.model, ''),
		       canary.position, canary.percent
		  FROM outbound_ai_routes route
		  JOIN outbound_ai_route_targets target ON target.route_id = route.id
		  LEFT JOIN outbound_ai_route_canaries canary ON canary.route_id = route.id AND canary.state = 'active'
		 WHERE route.id = $1
		 ORDER BY target.position`, id)
	if err != nil {
		return AIRoute{}, err
	}
	defer rows.Close()
	route := AIRoute{ID: routeID}
	for rows.Next() {
		var target AIRouteTarget
		var canaryPosition, canaryPercent *int
		if err := rows.Scan(&route.AccountID, &route.Format, &target.IntegrationID, &target.Model, &canaryPosition, &canaryPercent); err != nil {
			return AIRoute{}, err
		}
		route.Targets = append(route.Targets, target)
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
