package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ErrRouteAnalyticsTimeout reports a routes analytics read over retained
// request telemetry that ran past api.RouteAnalyticsQueryTimeout
// (ADR-20261026143210).
var ErrRouteAnalyticsTimeout = errors.New("route analytics read exceeded its time budget")

// routeAnalyticsRead runs fn in a read-only transaction whose
// statement_timeout is api.RouteAnalyticsQueryTimeout, so one large app's
// window cannot hold a Postgres backend for minutes. A cancelled statement
// returns ErrRouteAnalyticsTimeout.
func routeAnalyticsRead[T any](ctx context.Context, s *PgStore, fn func(sqlc.DBTX) (T, error)) (T, error) {
	return routeAnalyticsReadWithin(ctx, s, api.RouteAnalyticsQueryTimeout, fn)
}

func routeAnalyticsReadWithin[T any](ctx context.Context, s *PgStore, timeout time.Duration, fn func(sqlc.DBTX) (T, error)) (T, error) {
	var zero T
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return zero, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `select set_config('statement_timeout', $1, true)`,
		strconv.FormatInt(timeout.Milliseconds(), 10)); err != nil {
		return zero, mapErr(err)
	}
	out, err := fn(tx)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "57014" {
			return zero, ErrRouteAnalyticsTimeout
		}
		return zero, err
	}
	return out, mapErr(tx.Commit(ctx))
}

// RequestTelemetryCompleteness lists why retained request telemetry for an
// app may be missing requests in [from, until). It reads the coverage
// heartbeats route removal relies on: an active node with no or a stale
// heartbeat, telemetry off or sampled, rows still in a gateway backlog, or a
// loss or restart during the window. An empty list means every active node
// delivered the window.
func (s *PgStore) RequestTelemetryCompleteness(ctx context.Context, appID string, from, until time.Time) ([]string, error) {
	id, err := uuid.Parse(appID)
	if err != nil {
		return nil, fmt.Errorf("request telemetry completeness: app id: %w", err)
	}
	body, err := sqlc.New().RouteRemovalCoverageBlockers(ctx, s.pool, sqlc.RouteRemovalCoverageBlockersParams{
		AppID:       NewPgtypeUUID(id),
		WindowFrom:  pgtype.Timestamptz{Time: from, Valid: true},
		WindowUntil: pgtype.Timestamptz{Time: until, Valid: true},
	})
	if err != nil {
		return nil, mapErr(err)
	}
	gaps := []string{}
	if err := json.Unmarshal(body, &gaps); err != nil {
		return nil, fmt.Errorf("request telemetry completeness: decode: %w", err)
	}
	return gaps, nil
}
