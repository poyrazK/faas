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
	RouteAwakeWorkMinutes(context.Context, sqlc.RouteAwakeWorkMinutesParams) ([]sqlc.RouteAwakeWorkMinutesRow, error)
}

// Data is what Load reads for one window.
type Data struct {
	Awake   []Minute
	Routes  []RouteMinute
	Sources []SourceMinute
}

// Load reads an app's billed running minutes, the per-route minutes of its
// busiest routes, its probe and prewarm events, and the cron, queue and other
// deliveries schedd made to it (ADR-20261022184757) for [from, until).
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
	work, err := store.RouteAwakeWorkMinutes(ctx, sqlc.RouteAwakeWorkMinutesParams{
		AccountID: account, AppID: app, SinceAt: since, UntilAt: end, MaxMinutes: api.RouteAwakeWorkMaxMinutes,
	})
	if err != nil {
		return Data{}, fmt.Errorf("route awake work minutes: %w", err)
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
	for _, row := range work {
		out.Sources = append(out.Sources, SourceMinute{
			At: row.Minute.Time, Source: row.Source, Detail: row.Detail, Name: workName(row),
			Events: row.Started, Running: row.Running,
		})
	}
	return out, nil
}

// workName shows a cron job as the path or command it runs and its
// schedule, and a queue by its name.
func workName(row sqlc.RouteAwakeWorkMinutesRow) string {
	switch {
	case row.Source == api.RouteAwakeSourceCron && row.CronSchedule != "":
		return fmt.Sprintf("%s (%s)", row.CronTarget, row.CronSchedule)
	case row.Source == api.RouteAwakeSourceQueue:
		return row.Detail
	}
	return ""
}

func pgUUID(id string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("route awake: invalid id %q: %w", id, err)
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}
