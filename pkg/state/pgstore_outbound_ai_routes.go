package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ OutboundAIRouteStore = (*PgStore)(nil)

// ListOutboundAIRoutes returns an account's routes with ordered targets.
func (s *PgStore) ListOutboundAIRoutes(ctx context.Context, accountID string) ([]OutboundAIRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT route.id, route.name, route.format, route.created_at, route.updated_at, route.strategy, route.max_ttfb_ms,
		       target.integration_id, COALESCE(target.model, ''), target.weight, `+outboundAIRouteCanaryColumns+`
		  FROM outbound_ai_routes route
		  LEFT JOIN outbound_ai_route_targets target ON target.route_id = route.id
		  LEFT JOIN outbound_ai_route_canaries canary ON canary.route_id = route.id
		 WHERE route.account_id = $1
		 ORDER BY route.name, route.id, target.position`, mustPgUUID(accountID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]OutboundAIRoute, 0)
	index := map[string]int{}
	for rows.Next() {
		var routeID, integrationID pgtype.UUID
		var route OutboundAIRoute
		var model string
		var weight *int
		var canary outboundAIRouteCanaryScan
		if err := rows.Scan(append([]any{&routeID, &route.Name, &route.Format, &route.CreatedAt, &route.UpdatedAt, &route.Strategy,
			&route.MaxTTFBMS, &integrationID, &model, &weight}, canary.dest()...)...); err != nil {
			return nil, mapErr(err)
		}
		id := pgUUIDString(routeID)
		position, seen := index[id]
		if !seen {
			route.ID, route.AccountID, route.Canary = id, accountID, canary.canary()
			out = append(out, route)
			position = len(out) - 1
			index[id] = position
		}
		if integrationID.Valid {
			out[position].Targets = append(out[position].Targets, OutboundAIRouteTarget{IntegrationID: pgUUIDString(integrationID),
				Model: model, Weight: weight})
		}
	}
	return out, mapErr(rows.Err())
}

// GetOutboundAIRoute returns one account-owned route.
func (s *PgStore) GetOutboundAIRoute(ctx context.Context, accountID, routeID string) (OutboundAIRoute, error) {
	routes, err := s.ListOutboundAIRoutes(ctx, accountID)
	if err != nil {
		return OutboundAIRoute{}, err
	}
	for _, route := range routes {
		if route.ID == routeID {
			return route, nil
		}
	}
	return OutboundAIRoute{}, ErrNotFound
}

// outboundAIProviders loads the account's managed AI integrations named by
// targets, so route validation sees only integrations the account owns.
func outboundAIProviders(ctx context.Context, tx pgx.Tx, accountID string, targets []OutboundAIRouteTarget) (map[string]outboundAIRouteTarget, error) {
	ids := make([]pgtype.UUID, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, mustPgUUID(target.IntegrationID))
	}
	rows, err := tx.Query(ctx, `
		SELECT id, COALESCE(ai_provider, ''), allowed_path_prefixes FROM outbound_integrations
		 WHERE account_id = $1 AND id = ANY($2) AND provider_auth_mode = 'managed' AND enabled
		 FOR SHARE`, mustPgUUID(accountID), ids)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	providers := make(map[string]outboundAIRouteTarget, len(ids))
	for rows.Next() {
		var id pgtype.UUID
		var target outboundAIRouteTarget
		if err := rows.Scan(&id, &target.provider, &target.pathPrefixes); err != nil {
			return nil, mapErr(err)
		}
		providers[pgUUIDString(id)] = target
	}
	return providers, mapErr(rows.Err())
}

func insertOutboundAIRouteTargets(ctx context.Context, tx pgx.Tx, routeID string, targets []OutboundAIRouteTarget) error {
	for position, target := range targets {
		var model *string
		if target.Model != "" {
			model = &target.Model
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO outbound_ai_route_targets (route_id, position, integration_id, model, weight)
			VALUES ($1, $2, $3, $4, $5)`, mustPgUUID(routeID), position+1, mustPgUUID(target.IntegrationID), model, target.Weight); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// CreateOutboundAIRoute validates ownership and wire-format agreement of
// every target, then stores the route.
func (s *PgStore) CreateOutboundAIRoute(ctx context.Context, route OutboundAIRoute) (OutboundAIRoute, error) {
	format, ok := normalizeOutboundAIRouteFormat(route.Format)
	routing, routingOK := normalizeOutboundAIRouteRouting(OutboundAIRouteRouting{Strategy: route.Strategy, MaxTTFBMS: route.MaxTTFBMS}, route.Targets)
	if !ok || !routingOK || !api.ValidOutboundAIRouteName(route.Name) || !validOutboundAIRouteShape(route.Targets) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	route.Format, route.Strategy, route.MaxTTFBMS = format, routing.Strategy, routing.MaxTTFBMS
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM accounts WHERE id = $1 FOR UPDATE`, mustPgUUID(route.AccountID)).Scan(&locked); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM outbound_ai_routes WHERE account_id = $1`, mustPgUUID(route.AccountID)).Scan(&count); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	if count >= api.MaxOutboundAIRoutesPerAccount {
		return OutboundAIRoute{}, ErrOutboundAIRouteLimit
	}
	providers, err := outboundAIProviders(ctx, tx, route.AccountID, route.Targets)
	if err != nil {
		return OutboundAIRoute{}, err
	}
	if !outboundAIRouteTargetsAllowed(route.Format, route.Targets, providers) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_routes (id, account_id, name, format, strategy, max_ttfb_ms) VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`, mustPgUUID(route.ID), mustPgUUID(route.AccountID), route.Name, route.Format,
		route.Strategy, route.MaxTTFBMS).
		Scan(&route.CreatedAt, &route.UpdatedAt); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	if err := insertOutboundAIRouteTargets(ctx, tx, route.ID, route.Targets); err != nil {
		return OutboundAIRoute{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	return copyOutboundAIRoute(route), nil
}

// SetOutboundAIRouteTargets replaces a route's ordered targets and its
// strategy (ADR-1434).
func (s *PgStore) SetOutboundAIRouteTargets(ctx context.Context, accountID, routeID string, targets []OutboundAIRouteTarget,
	routing OutboundAIRouteRouting) error {
	routing, routingOK := normalizeOutboundAIRouteRouting(routing, targets)
	if !routingOK || !validOutboundAIRouteShape(targets) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var format string
	if err := tx.QueryRow(ctx, `SELECT format FROM outbound_ai_routes WHERE account_id = $1 AND id = $2 FOR UPDATE`,
		mustPgUUID(accountID), mustPgUUID(routeID)).Scan(&format); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return mapErr(err)
	}
	providers, err := outboundAIProviders(ctx, tx, accountID, targets)
	if err != nil {
		return err
	}
	if !outboundAIRouteTargetsAllowed(format, targets, providers) {
		return ErrInvalidArgument
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_targets WHERE route_id = $1`, mustPgUUID(routeID)); err != nil {
		return mapErr(err)
	}
	if err := insertOutboundAIRouteTargets(ctx, tx, routeID, targets); err != nil {
		return err
	}
	// ADR-1340: new targets end the canary and clear the per-target counts;
	// their positions named the old targets (ADR-1434).
	if _, err := endOutboundAIRouteCanary(ctx, tx, routeID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_ai_routes SET strategy = $2, max_ttfb_ms = $3, updated_at = now() WHERE id = $1`,
		mustPgUUID(routeID), routing.Strategy, routing.MaxTTFBMS); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// OutboundAIRouteStats returns each target's settled attempts over the
// strategy window, from the minute the window starts (ADR-1434).
func (s *PgStore) OutboundAIRouteStats(ctx context.Context, accountID, routeID string) (OutboundAIRouteStatsWindow, error) {
	var window OutboundAIRouteStatsWindow
	var targets int
	err := s.pool.QueryRow(ctx, `
		SELECT route.strategy, (SELECT count(*) FROM outbound_ai_route_targets target WHERE target.route_id = route.id),
		       date_trunc('minute', now() - make_interval(mins => $3::int))
		  FROM outbound_ai_routes route WHERE route.account_id = $1 AND route.id = $2`,
		mustPgUUID(accountID), mustPgUUID(routeID), api.OutboundAIRouteStatsWindowMinutes).Scan(&window.Strategy, &targets, &window.WindowStart)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIRouteStatsWindow{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIRouteStatsWindow{}, mapErr(err)
	}
	window.WindowStart = window.WindowStart.UTC()
	window.Targets = make([]OutboundAIRouteTargetStats, targets)
	for index := range window.Targets {
		window.Targets[index].Position = index + 1
	}
	rows, err := s.pool.Query(ctx, `
		SELECT position, sum(calls)::bigint, sum(errors)::bigint, sum(ttfb_calls)::bigint, sum(ttfb_ms_total)::bigint,
		       sum(cost_millicents)::bigint
		  FROM outbound_ai_route_target_minutes
		 WHERE route_id = $1 AND minute >= $2
		 GROUP BY position`, mustPgUUID(routeID), window.WindowStart)
	if err != nil {
		return OutboundAIRouteStatsWindow{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var stats OutboundAIRouteTargetStats
		if err := rows.Scan(&stats.Position, &stats.Calls, &stats.Errors, &stats.TTFBCalls, &stats.TTFBMSTotal, &stats.CostMillicents); err != nil {
			return OutboundAIRouteStatsWindow{}, mapErr(err)
		}
		if stats.Position >= 1 && stats.Position <= targets {
			window.Targets[stats.Position-1] = stats
		}
	}
	return window, mapErr(rows.Err())
}

// DeleteOutboundAIRoute removes a route, its targets and canary, and the
// canary's per-minute rows, which have no foreign key (ADR-1340).
func (s *PgStore) DeleteOutboundAIRoute(ctx context.Context, accountID, routeID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `DELETE FROM outbound_ai_routes WHERE account_id = $1 AND id = $2`,
		mustPgUUID(accountID), mustPgUUID(routeID))
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_target_minutes WHERE route_id = $1`, mustPgUUID(routeID)); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}
