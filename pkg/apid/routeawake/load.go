package routeawake

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Store is the PgStore surface the report reads.
type Store interface {
	RouteAwakeMinutes(context.Context, sqlc.RouteAwakeMinutesParams) ([]sqlc.RouteAwakeMinutesRow, error)
	RouteAwakeRouteMinutes(context.Context, sqlc.RouteAwakeRouteMinutesParams) ([]sqlc.RouteAwakeRouteMinutesRow, error)
}

// Load reads an app's billed running minutes and the per-route minutes of
// its busiest routes for [from, until).
func Load(ctx context.Context, store Store, accountID, appID string, from, until time.Time) ([]Minute, []RouteMinute, error) {
	account, err := pgUUID(accountID)
	if err != nil {
		return nil, nil, err
	}
	app, err := pgUUID(appID)
	if err != nil {
		return nil, nil, err
	}
	since, end := pgtype.Timestamptz{Time: from, Valid: true}, pgtype.Timestamptz{Time: until, Valid: true}
	usage, err := store.RouteAwakeMinutes(ctx, sqlc.RouteAwakeMinutesParams{AccountID: account, AppID: app, SinceAt: since, UntilAt: end})
	if err != nil {
		return nil, nil, fmt.Errorf("route awake minutes: %w", err)
	}
	rows, err := store.RouteAwakeRouteMinutes(ctx, sqlc.RouteAwakeRouteMinutesParams{
		AccountID: account, AppID: app, SinceAt: since, UntilAt: end, RouteLimit: api.RouteAwakeMaxRoutes,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("route awake route minutes: %w", err)
	}
	awake := make([]Minute, 0, len(usage))
	for _, row := range usage {
		awake = append(awake, Minute{At: row.Minute.Time, MBSeconds: row.MbSeconds, ColdBoots: row.ColdBoots})
	}
	routes := make([]RouteMinute, 0, len(rows))
	for _, row := range rows {
		routes = append(routes, RouteMinute{
			At: row.Minute.Time, Method: row.Method, Path: strings.TrimPrefix(row.Route, row.Method+" "),
			Requests: row.Requests, ColdBoots: row.ColdBoots, AnonymousRequests: row.AnonymousRequests,
		})
	}
	return awake, routes, nil
}

func pgUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("route awake: invalid id %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
