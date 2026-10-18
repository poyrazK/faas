package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// outboundAIRouteCanaryColumns are read with a route through
// `LEFT JOIN outbound_ai_route_canaries canary`; all are NULL without one.
const outboundAIRouteCanaryColumns = `canary.position, canary.percent, canary.max_error_rate_increase,
       canary.max_latency_increase_percent, canary.min_calls, canary.window_minutes, canary.state,
       canary.reason, canary.started_at, canary.decided_at`

// outboundAIRouteCanaryScan receives outboundAIRouteCanaryColumns.
type outboundAIRouteCanaryScan struct {
	position, percent, maxErrorRateIncrease, maxLatencyIncreasePercent, minCalls, windowMinutes *int
	state, reason                                                                               *string
	startedAt, decidedAt                                                                        *time.Time
}

func (c *outboundAIRouteCanaryScan) dest() []any {
	return []any{&c.position, &c.percent, &c.maxErrorRateIncrease, &c.maxLatencyIncreasePercent, &c.minCalls,
		&c.windowMinutes, &c.state, &c.reason, &c.startedAt, &c.decidedAt}
}

func (c *outboundAIRouteCanaryScan) canary() *OutboundAIRouteCanary {
	if c.position == nil {
		return nil
	}
	canary := &OutboundAIRouteCanary{
		Position: *c.position, Percent: *c.percent, MaxErrorRateIncrease: *c.maxErrorRateIncrease,
		MaxLatencyIncreasePercent: c.maxLatencyIncreasePercent, MinCalls: *c.minCalls, WindowMinutes: *c.windowMinutes,
		State: *c.state, StartedAt: c.startedAt.UTC(), DecidedAt: c.decidedAt,
	}
	if c.reason != nil {
		canary.Reason = *c.reason
	}
	return canary
}

// lockOutboundAIRoute locks an account's route and returns its target count.
func lockOutboundAIRoute(ctx context.Context, tx pgx.Tx, accountID, routeID string) (int, error) {
	var targets int
	err := tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM outbound_ai_route_targets target WHERE target.route_id = route.id)
		  FROM outbound_ai_routes route WHERE route.account_id = $1 AND route.id = $2 FOR UPDATE`,
		mustPgUUID(accountID), mustPgUUID(routeID)).Scan(&targets)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return targets, mapErr(err)
}

// endOutboundAIRouteCanary deletes a route's canary and its counts and
// reports whether there was a canary.
func endOutboundAIRouteCanary(ctx context.Context, tx pgx.Tx, routeID string) (bool, error) {
	command, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_canaries WHERE route_id = $1`, mustPgUUID(routeID))
	if err != nil {
		return false, mapErr(err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_target_minutes WHERE route_id = $1`, mustPgUUID(routeID)); err != nil {
		return false, mapErr(err)
	}
	return command.RowsAffected() == 1, nil
}

// SetOutboundAIRouteCanary starts or replaces a route's canary (ADR-1340).
func (s *PgStore) SetOutboundAIRouteCanary(ctx context.Context, accountID, routeID string, canary OutboundAIRouteCanary) (OutboundAIRouteCanary, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIRouteCanary{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	targets, err := lockOutboundAIRoute(ctx, tx, accountID, routeID)
	if err != nil {
		return OutboundAIRouteCanary{}, err
	}
	if !validOutboundAIRouteCanary(canary, targets) {
		return OutboundAIRouteCanary{}, ErrInvalidArgument
	}
	if _, err := endOutboundAIRouteCanary(ctx, tx, routeID); err != nil {
		return OutboundAIRouteCanary{}, err
	}
	if err := tx.QueryRow(ctx, `
		INSERT INTO outbound_ai_route_canaries
		    (route_id, position, percent, max_error_rate_increase, max_latency_increase_percent, min_calls, window_minutes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING started_at`, mustPgUUID(routeID), canary.Position, canary.Percent, canary.MaxErrorRateIncrease,
		canary.MaxLatencyIncreasePercent, canary.MinCalls, canary.WindowMinutes).Scan(&canary.StartedAt); err != nil {
		return OutboundAIRouteCanary{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIRouteCanary{}, mapErr(err)
	}
	canary.State, canary.Reason, canary.DecidedAt, canary.StartedAt = api.OutboundAIRouteCanaryActive, "", nil, canary.StartedAt.UTC()
	return canary, nil
}

// DeleteOutboundAIRouteCanary ends a route's canary. A route without one
// reports ErrNotFound.
func (s *PgStore) DeleteOutboundAIRouteCanary(ctx context.Context, accountID, routeID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockOutboundAIRoute(ctx, tx, accountID, routeID); err != nil {
		return err
	}
	ended, err := endOutboundAIRouteCanary(ctx, tx, routeID)
	if err != nil {
		return err
	}
	if !ended {
		return ErrNotFound
	}
	return mapErr(tx.Commit(ctx))
}

// PromoteOutboundAIRouteCanary moves the canary's target to the front of
// the route and ends the canary.
func (s *PgStore) PromoteOutboundAIRouteCanary(ctx context.Context, accountID, routeID string) (OutboundAIRoute, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := lockOutboundAIRoute(ctx, tx, accountID, routeID); err != nil {
		return OutboundAIRoute{}, err
	}
	var position int
	err = tx.QueryRow(ctx, `SELECT position FROM outbound_ai_route_canaries WHERE route_id = $1`, mustPgUUID(routeID)).Scan(&position)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIRoute{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	targets, err := outboundAIRouteTargetsTx(ctx, tx, routeID)
	if err != nil {
		return OutboundAIRoute{}, err
	}
	if position > len(targets) {
		return OutboundAIRoute{}, ErrInvalidArgument
	}
	if _, err := endOutboundAIRouteCanary(ctx, tx, routeID); err != nil {
		return OutboundAIRoute{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_ai_route_targets WHERE route_id = $1`, mustPgUUID(routeID)); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	if err := insertOutboundAIRouteTargets(ctx, tx, routeID, promotedOutboundAIRouteTargets(targets, position)); err != nil {
		return OutboundAIRoute{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE outbound_ai_routes SET updated_at = now() WHERE id = $1`, mustPgUUID(routeID)); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OutboundAIRoute{}, mapErr(err)
	}
	return s.GetOutboundAIRoute(ctx, accountID, routeID)
}

func outboundAIRouteTargetsTx(ctx context.Context, tx pgx.Tx, routeID string) ([]OutboundAIRouteTarget, error) {
	rows, err := tx.Query(ctx, `
		SELECT integration_id::text, COALESCE(model, ''), weight FROM outbound_ai_route_targets
		 WHERE route_id = $1 ORDER BY position`, mustPgUUID(routeID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var targets []OutboundAIRouteTarget
	for rows.Next() {
		var target OutboundAIRouteTarget
		if err := rows.Scan(&target.IntegrationID, &target.Model, &target.Weight); err != nil {
			return nil, mapErr(err)
		}
		targets = append(targets, target)
	}
	return targets, mapErr(rows.Err())
}

// OutboundAIRouteCanaryStatus returns a route's canary with the first
// target's and the canary target's counts over its window: from the later
// of the canary's start and the window's start, by whole minutes, up to the
// minute it was decided, since latency and cost routes keep counting
// (ADR-1434).
func (s *PgStore) OutboundAIRouteCanaryStatus(ctx context.Context, accountID, routeID string) (OutboundAIRouteCanaryStatus, error) {
	var scan outboundAIRouteCanaryScan
	var windowStart time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT `+outboundAIRouteCanaryColumns+`,
		       greatest(canary.started_at, now() - make_interval(mins => canary.window_minutes::int))
		  FROM outbound_ai_routes route
		  JOIN outbound_ai_route_canaries canary ON canary.route_id = route.id
		 WHERE route.account_id = $1 AND route.id = $2`, mustPgUUID(accountID), mustPgUUID(routeID)).
		Scan(append(scan.dest(), &windowStart)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return OutboundAIRouteCanaryStatus{}, ErrNotFound
	}
	if err != nil {
		return OutboundAIRouteCanaryStatus{}, mapErr(err)
	}
	canary := scan.canary()
	status := OutboundAIRouteCanaryStatus{Canary: *canary, WindowStart: windowStart.UTC(),
		Baseline: OutboundAIRouteTargetStats{Position: 1}, CanaryStats: OutboundAIRouteTargetStats{Position: canary.Position}}
	rows, err := s.pool.Query(ctx, `
		SELECT position, sum(calls)::bigint, sum(errors)::bigint, sum(ttfb_calls)::bigint, sum(ttfb_ms_total)::bigint,
		       sum(cost_millicents)::bigint
		  FROM outbound_ai_route_target_minutes
		 WHERE route_id = $1 AND position IN (1, $2) AND minute >= date_trunc('minute', $3::timestamptz)
		   AND ($4::timestamptz IS NULL OR minute <= date_trunc('minute', $4::timestamptz))
		 GROUP BY position`, mustPgUUID(routeID), canary.Position, windowStart, canary.DecidedAt)
	if err != nil {
		return OutboundAIRouteCanaryStatus{}, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var stats OutboundAIRouteTargetStats
		if err := rows.Scan(&stats.Position, &stats.Calls, &stats.Errors, &stats.TTFBCalls, &stats.TTFBMSTotal, &stats.CostMillicents); err != nil {
			return OutboundAIRouteCanaryStatus{}, mapErr(err)
		}
		if stats.Position == 1 {
			status.Baseline = stats
		} else {
			status.CanaryStats = stats
		}
	}
	return status, mapErr(rows.Err())
}
