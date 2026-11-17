package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

// These reads use the same unsampled rollup population as route totals.
// CPU remains optional sampled evidence joined by an explicit deployment ID.
func (s *PgStore) RequestAnalyticsRollupByDeployment(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsByDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, error) {
	hoursFrom, hoursUntil, err := s.requestAnalyticsDeploymentHours(ctx, arg.AppID, arg.ReceivedAt.Time, arg.ReceivedAt_2.Time)
	if err != nil {
		return nil, err
	}
	rows, err := routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RequestAnalyticsRollupByDeploymentRow, error) {
		return sqlc.New().RequestAnalyticsRollupByDeployment(ctx, db, sqlc.RequestAnalyticsRollupByDeploymentParams{HoursFrom: hoursFrom, HoursUntil: hoursUntil, AppID: arg.AppID, AccountID: arg.AccountID, WindowFrom: arg.ReceivedAt, WindowUntil: arg.ReceivedAt_2, DeploymentLimit: arg.Limit})
	})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, sqlc.RequestTelemetryAnalyticsByDeploymentRow{DeploymentID: r.DeploymentID, CommitSha: r.CommitSha, DeploymentTag: r.DeploymentTag, DeploymentCreatedAt: r.DeploymentCreatedAt, Requests: r.Requests, TotalRequests: r.TotalRequests})
	}
	return out, nil
}

func (s *PgStore) RequestAnalyticsRollupByRouteDeployment(ctx context.Context, arg sqlc.RequestTelemetryAnalyticsByRouteDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow, error) {
	hoursFrom, hoursUntil, err := s.requestAnalyticsDeploymentHours(ctx, arg.AppID, arg.ReceivedAt.Time, arg.ReceivedAt_2.Time)
	if err != nil {
		return nil, err
	}
	rows, err := routeAnalyticsRead(ctx, s, func(db sqlc.DBTX) ([]sqlc.RequestAnalyticsRollupByRouteDeploymentRow, error) {
		return sqlc.New().RequestAnalyticsRollupByRouteDeployment(ctx, db, sqlc.RequestAnalyticsRollupByRouteDeploymentParams{HoursFrom: hoursFrom, HoursUntil: hoursUntil, AppID: arg.AppID, AccountID: arg.AccountID, WindowFrom: arg.ReceivedAt, WindowUntil: arg.ReceivedAt_2, DeploymentLimit: arg.DeploymentLimit, RouteLimit: arg.RouteLimit})
	})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow{Route: r.Route, Method: r.Method, DeploymentID: r.DeploymentID, CommitSha: r.CommitSha, DeploymentTag: r.DeploymentTag, DeploymentCreatedAt: r.DeploymentCreatedAt, Requests: r.Requests})
	}
	return out, nil
}

// Match the hourly/minute partition used by analyticsrollup.HourRange without
// coupling the state layer to apid. Empty hourly coverage reads minutes only.
func (s *PgStore) requestAnalyticsDeploymentHours(ctx context.Context, id pgtype.UUID, from, until time.Time) (pgtype.Timestamptz, pgtype.Timestamptz, error) {
	start, end := until, until
	since, has, err := s.RequestAnalyticsRollupHoursCoverage(ctx, uuid.UUID(id.Bytes))
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.Timestamptz{}, err
	}
	if has {
		if from.Before(since) {
			quiet, readErr := s.RequestTelemetryExistsBetween(ctx, uuid.UUID(id.Bytes), from, since)
			if readErr != nil {
				return pgtype.Timestamptz{}, pgtype.Timestamptz{}, readErr
			}
			if !quiet {
				since = from.Add(-api.RequestAnalyticsSettleLag)
			}
		}
		if settled := since.Add(api.RequestAnalyticsSettleLag); settled.After(from) {
			from = settled
		}
		start = from.UTC().Truncate(time.Hour)
		if start.Before(from) {
			start = start.Add(time.Hour)
		}
		end = until.UTC().Truncate(time.Hour)
		if !start.Before(end) {
			start, end = until, until
		}
	}
	return pgtype.Timestamptz{Time: start, Valid: true}, pgtype.Timestamptz{Time: end, Valid: true}, nil
}
