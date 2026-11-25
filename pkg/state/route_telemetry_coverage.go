package state

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// routeMonitorTelemetryUnavailable is the production monitor's reason for
// evidence a gateway may have lost (ADR-20261028110044 amendment 1): a
// report reason every client already accepts.
const routeMonitorTelemetryUnavailable = "telemetry_unavailable"

// pgRouteTelemetryLost reports whether a gateway may have lost request
// telemetry in the closed windows route evidence reads at now. Evidence over
// a gap would compare partial counts, so callers report it unavailable.
func pgRouteTelemetryLost(ctx context.Context, db sqlc.DBTX, now time.Time) (bool, error) {
	windows := routehealth.Windows(now)
	from := windows[0].Start
	lost, err := sqlc.New().RouteTelemetryLost(ctx, db, sqlc.RouteTelemetryLostParams{
		ReportedSince: pgTimestamptzUTC(from.Add(-api.RouteTelemetrySilenceGrace)),
		WindowFrom:    pgTimestamptzUTC(from), WindowUntil: pgTimestamptzUTC(windows[len(windows)-1].End),
	})
	if err != nil {
		return false, fmt.Errorf("read request telemetry coverage: %w", err)
	}
	return lost, nil
}

// RequestTelemetryLossReader reports whether any active gateway may have lost
// request telemetry over [from, until). The release guards (ADR-625, the
// post-release watch and the canary circuit breaker) hold their decision over
// such a window: missing 2xx rows read as a higher error rate (ADR-625
// amendment 2026-11-10). Request telemetry is Postgres-only, so MemStore does
// not implement it and callers treat its absence as complete coverage.
type RequestTelemetryLossReader interface {
	RequestTelemetryLostBetween(ctx context.Context, from, until time.Time) (bool, error)
}

// RequestTelemetryLostBetween implements RequestTelemetryLossReader with the
// coverage rule route evidence uses.
func (s *PgStore) RequestTelemetryLostBetween(ctx context.Context, from, until time.Time) (bool, error) {
	if !from.Before(until) {
		return false, nil
	}
	lost, err := sqlc.New().RouteTelemetryLost(ctx, s.pool, sqlc.RouteTelemetryLostParams{
		ReportedSince: pgTimestamptzUTC(from.Add(-api.RouteTelemetrySilenceGrace)),
		WindowFrom:    pgTimestamptzUTC(from), WindowUntil: pgTimestamptzUTC(until),
	})
	if err != nil {
		return false, fmt.Errorf("state: read request telemetry coverage: %w", err)
	}
	return lost, nil
}

// RequestTelemetryCoverageReader finds the part of a release guard's window
// that request telemetry fully covers. A gateway restart that was not clean
// begins a new coverage interval; a guard holding its whole window would
// hold until an operator stepped in, so it judges the window's tail instead.
type RequestTelemetryCoverageReader interface {
	RequestTelemetryCoveredFrom(ctx context.Context, from, until time.Time) (time.Time, bool, error)
}

// RequestTelemetryCoveredFrom returns the start of the longest tail of
// [from, until) no gateway may have lost telemetry from: from itself when the
// whole window is covered, else the first minute after the latest gateway's
// coverage began. ok is false when no tail is covered.
func (s *PgStore) RequestTelemetryCoveredFrom(ctx context.Context, from, until time.Time) (time.Time, bool, error) {
	lost, err := s.RequestTelemetryLostBetween(ctx, from, until)
	if err != nil || !lost {
		return from, err == nil, err
	}
	row, err := sqlc.New().RequestTelemetryCoveredSince(ctx, s.pool, pgTimestamptzUTC(until.Add(-api.RouteTelemetrySilenceGrace)))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("state: read request telemetry coverage start: %w", err)
	}
	if row.Nodes == 0 || !row.Healthy || !row.CoveredSince.Valid {
		return time.Time{}, false, nil
	}
	// Telemetry rows are minute buckets: the minute coverage began in may
	// hold rows recorded before it.
	tail := row.CoveredSince.Time.UTC().Truncate(time.Minute).Add(time.Minute)
	if !tail.After(from) || !tail.Before(until) {
		return time.Time{}, false, nil
	}
	if lost, err = s.RequestTelemetryLostBetween(ctx, tail, until); err != nil || lost {
		return time.Time{}, false, err
	}
	return tail, true, nil
}
