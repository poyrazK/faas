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
		SELECT route.id, route.name, route.created_at, route.updated_at,
		       target.integration_id, COALESCE(target.model, '')
		  FROM outbound_ai_routes route
		  LEFT JOIN outbound_ai_route_targets target ON target.route_id = route.id
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
		if err := rows.Scan(&routeID, &route.Name, &route.CreatedAt, &route.UpdatedAt, &integrationID, &model); err != nil {
			return nil, mapErr(err)
		}
		id := pgUUIDString(routeID)
		position, seen := index[id]
		if !seen {
			route.ID, route.AccountID = id, accountID
			out = append(out, route)
			position = len(out) - 1
			index[id] = position
		}
		if integrationID.Valid {
			out[position].Targets = append(out[position].Targets, OutboundAIRouteTarget{IntegrationID: pgUUIDString(integrationID), Model: model})
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
func outboundAIProviders(ctx context.Context, tx pgx.Tx, accountID string, targets []OutboundAIRouteTarget) (map[string]string, error) {
	ids := make([]pgtype.UUID, 0, len(targets))
	for _, target := range targets {
		ids = append(ids, mustPgUUID(target.IntegrationID))
	}
	rows, err := tx.Query(ctx, `
		SELECT id, COALESCE(ai_provider, '') FROM outbound_integrations
		 WHERE account_id = $1 AND id = ANY($2) AND provider_auth_mode = 'managed' AND enabled
		 FOR SHARE`, mustPgUUID(accountID), ids)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	providers := make(map[string]string, len(ids))
	for rows.Next() {
		var id pgtype.UUID
		var provider string
		if err := rows.Scan(&id, &provider); err != nil {
			return nil, mapErr(err)
		}
		providers[pgUUIDString(id)] = provider
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
			INSERT INTO outbound_ai_route_targets (route_id, position, integration_id, model)
			VALUES ($1, $2, $3, $4)`, mustPgUUID(routeID), position+1, mustPgUUID(target.IntegrationID), model); err != nil {
			return mapErr(err)
		}
	}
	return nil
}

// CreateOutboundAIRoute validates ownership and wire-format agreement of
// every target, then stores the route.
func (s *PgStore) CreateOutboundAIRoute(ctx context.Context, route OutboundAIRoute) (OutboundAIRoute, error) {
	if !api.ValidOutboundAIRouteName(route.Name) || !validOutboundAIRouteShape(route.Targets) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
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
	if !sameOutboundAIProvider(route.Targets, providers) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_routes (id, account_id, name) VALUES ($1, $2, $3)
		RETURNING created_at, updated_at`, mustPgUUID(route.ID), mustPgUUID(route.AccountID), route.Name).
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

// SetOutboundAIRouteTargets replaces a route's ordered targets.
func (s *PgStore) SetOutboundAIRouteTargets(ctx context.Context, accountID, routeID string, targets []OutboundAIRouteTarget) error {
	if !validOutboundAIRouteShape(targets) {
		return ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked pgtype.UUID
	if err := tx.QueryRow(ctx, `SELECT id FROM outbound_ai_routes WHERE account_id = $1 AND id = $2 FOR UPDATE`,
		mustPgUUID(accountID), mustPgUUID(routeID)).Scan(&locked); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return mapErr(err)
	}
	providers, err := outboundAIProviders(ctx, tx, accountID, targets)
	if err != nil {
		return err
	}
	if !sameOutboundAIProvider(targets, providers) {
		return ErrInvalidArgument
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_targets WHERE route_id = $1`, mustPgUUID(routeID)); err != nil {
		return mapErr(err)
	}
	if err := insertOutboundAIRouteTargets(ctx, tx, routeID, targets); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_ai_routes SET updated_at = now() WHERE id = $1`, mustPgUUID(routeID)); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// DeleteOutboundAIRoute removes a route and its targets.
func (s *PgStore) DeleteOutboundAIRoute(ctx context.Context, accountID, routeID string) error {
	command, err := s.pool.Exec(ctx, `DELETE FROM outbound_ai_routes WHERE account_id = $1 AND id = $2`,
		mustPgUUID(accountID), mustPgUUID(routeID))
	if err != nil {
		return mapErr(err)
	}
	if command.RowsAffected() != 1 {
		return ErrNotFound
	}
	return nil
}
