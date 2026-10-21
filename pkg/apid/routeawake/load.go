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
	RouteAwakeSourceMinutes(context.Context, sqlc.RouteAwakeSourceMinutesParams) ([]sqlc.RouteAwakeSourceMinutesRow, error)
}

// Data is what Load reads for one window.
type Data struct {
	Awake   []Minute
	Routes  []RouteMinute
	Sources []SourceMinute
}

// Load reads an app's billed running minutes, the per-route minutes of its
// busiest routes and its probe and prewarm events for [from, until).
func Load(ctx context.Context, store Store, accountID, appID string, from, until time.Time) (Data, error) {
	account, err := pgUUID(accountID)
	if err != nil {
		return Data{}, err
	}
	app, err := pgUUID(appID)
	if err != nil {
		return Data{}, err
	}
	since, end := pgtype.Timestamptz{Time: from, Valid: true}, pgtype.Timestamptz{Time: until, Valid: true}
	usage, err := store.RouteAwakeMinutes(ctx, sqlc.RouteAwakeMinutesParams{AccountID: account, AppID: app, SinceAt: since, UntilAt: end})
	if err != nil {
		return Data{}, fmt.Errorf("route awake minutes: %w", err)
	}
	rows, err := store.RouteAwakeRouteMinutes(ctx, sqlc.RouteAwakeRouteMinutesParams{
		AccountID: account, AppID: app, SinceAt: since, UntilAt: end, RouteLimit: api.RouteAwakeMaxRoutes,
	})
	if err != nil {
		return Data{}, fmt.Errorf("route awake route minutes: %w", err)
	}
	sources, err := store.RouteAwakeSourceMinutes(ctx, sqlc.RouteAwakeSourceMinutesParams{AccountID: account, AppID: app, SinceAt: since, UntilAt: end})
	if err != nil {
		return Data{}, fmt.Errorf("route awake source minutes: %w", err)
	}
	var out Data
	for _, row := range usage {
		out.Awake = append(out.Awake, Minute{At: row.Minute.Time, MBSeconds: row.MbSeconds, ColdBoots: row.ColdBoots})
	}
	for _, row := range rows {
		out.Routes = append(out.Routes, RouteMinute{
			At: row.Minute.Time, Method: row.Method, Path: strings.TrimPrefix(row.Route, row.Method+" "),
			Requests: row.Requests, ColdBoots: row.ColdBoots, AnonymousRequests: row.AnonymousRequests,
		})
	}
	for _, row := range sources {
		out.Sources = append(out.Sources, SourceMinute{At: row.Minute.Time, Source: row.Source, Events: row.Events})
	}
	return out, nil
}

func pgUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("route awake: invalid id %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
