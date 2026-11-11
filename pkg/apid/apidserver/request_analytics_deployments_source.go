package apidserver

import (
	"context"
	"github.com/onebox-faas/faas/pkg/apid/analyticsrollup"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type analyticsDeploymentRollups interface {
	RequestAnalyticsRollupByDeployment(context.Context, sqlc.RequestTelemetryAnalyticsByDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, error)
	RequestAnalyticsRollupByRouteDeployment(context.Context, sqlc.RequestTelemetryAnalyticsByRouteDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow, error)
}

func (s *server) requestAnalyticsDeploymentRows(ctx context.Context, raw requestAnalyticsDeploymentReader, app state.App, acct state.Account, window requestAnalyticsWindow, arg sqlc.RequestTelemetryAnalyticsByDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, error) {
	rollups, ok := s.store.(analyticsDeploymentRollups)
	if !ok {
		return raw.RequestTelemetryAnalyticsByDeployment(ctx, arg)
	}
	if _, covered := analyticsrollup.Covers(ctx, s.store, app.ID, window.From); !covered {
		return raw.RequestTelemetryAnalyticsByDeployment(ctx, arg)
	}
	rows, err := rollups.RequestAnalyticsRollupByDeployment(ctx, arg)
	if err != nil {
		return nil, err
	}
	samples, sampleErr := analyticsrollup.OptionalRead(ctx, func(ctx context.Context) ([]sqlc.RequestTelemetryAnalyticsByDeploymentRow, error) {
		return raw.RequestTelemetryAnalyticsByDeployment(ctx, arg)
	})
	if sampleErr == nil {
		for i := range rows {
			for _, sample := range samples {
				if rows[i].DeploymentID == sample.DeploymentID {
					rows[i].GuestCpuMeasuredRequests = sample.GuestCpuMeasuredRequests
					rows[i].GuestCpuAvgMs = sample.GuestCpuAvgMs
				}
			}
		}
	}
	return rows, nil
}

func (s *server) requestAnalyticsRouteDeploymentRows(ctx context.Context, raw requestAnalyticsRouteDeploymentReader, app state.App, acct state.Account, window requestAnalyticsWindow, arg sqlc.RequestTelemetryAnalyticsByRouteDeploymentParams) ([]sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow, error) {
	rollups, ok := s.store.(analyticsDeploymentRollups)
	if !ok {
		return raw.RequestTelemetryAnalyticsByRouteDeployment(ctx, arg)
	}
	if _, covered := analyticsrollup.Covers(ctx, s.store, app.ID, window.From); !covered {
		return raw.RequestTelemetryAnalyticsByRouteDeployment(ctx, arg)
	}
	rows, err := rollups.RequestAnalyticsRollupByRouteDeployment(ctx, arg)
	if err != nil {
		return nil, err
	}
	samples, sampleErr := analyticsrollup.OptionalRead(ctx, func(ctx context.Context) ([]sqlc.RequestTelemetryAnalyticsByRouteDeploymentRow, error) {
		return raw.RequestTelemetryAnalyticsByRouteDeployment(ctx, arg)
	})
	if sampleErr == nil {
		for i := range rows {
			if rows[i].DeploymentID == "__other__" {
				continue
			}
			for _, sample := range samples {
				if rows[i].Route == sample.Route && rows[i].Method == sample.Method && rows[i].DeploymentID == sample.DeploymentID {
					rows[i].GuestCpuMeasuredRequests = sample.GuestCpuMeasuredRequests
					rows[i].GuestCpuAvgMs = sample.GuestCpuAvgMs
				}
			}
		}
	}
	return rows, nil
}
