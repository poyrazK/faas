package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectRollbackLiveTrafficPolicy pins the error budgets used by a rollback.
type ProjectRollbackLiveTrafficPolicy struct {
	Revision int64                   `json:"revision"`
	Routes   []api.RouteMonitorRoute `json:"routes"`
}

type ProjectRollbackLiveTrafficStore interface {
	GetRouteMonitor(context.Context, string, string) (api.RouteMonitorConfig, error)
	ProjectRollbackLiveTrafficReport(context.Context, string, api.ProjectRollbackStep, ProjectRollbackLiveTrafficPolicy) (api.RouteMonitorReport, error)
}

func (s *PgStore) ProjectRollbackLiveTrafficReport(ctx context.Context, operation string, step api.ProjectRollbackStep, policy ProjectRollbackLiveTrafficPolicy) (api.RouteMonitorReport, error) {
	var report api.RouteMonitorReport
	if step.LiveTraffic == nil || step.LiveTraffic.RouteGeneration == nil || len(policy.Routes) == 0 {
		return report, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return report, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var account string
	var anchor time.Time
	err = tx.QueryRow(ctx, `
		SELECT a.account_id::text, r.gateways_applied_at FROM project_rollback_http_recovery r
		JOIN apps a ON a.id = r.app_id
		WHERE r.operation_id = $1 AND r.rollback_id = $2 AND r.app_id = $3 AND r.target_id = $4
		  AND r.generation = $5 AND r.gateways_applied_at IS NOT NULL AND `+projectRollbackRouteCurrent,
		operation, step.AttemptID, step.AppID, step.TargetID, *step.LiveTraffic.RouteGeneration).Scan(&account, &anchor)
	if err != nil {
		return report, mapErr(err)
	}
	owner, err := pgRouteMonitorOwner(ctx, tx, account, step.AppID, false)
	if err != nil {
		return report, err
	}
	now, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return report, err
	}
	report = routemonitor.NewReport(api.RouteMonitorConfig{
		AppID: step.AppID, Enabled: true, Revision: policy.Revision, Routes: policy.Routes,
	}, now.Time)
	if step.LiveTraffic.ObserveAfter != nil && step.LiveTraffic.ObserveAfter.After(anchor) {
		anchor = *step.LiveTraffic.ObserveAfter
	}
	report.DeploymentID, report.ObservationAnchor = step.TargetID, &anchor
	unavailable := ""
	switch {
	case !owner.Account.Plan.DebugTelemetryEnabled():
		unavailable = "telemetry_not_entitled"
	case !owner.Account.MayDeploy():
		unavailable = "account_ineligible"
	case !routehealth.Windows(now.Time)[0].Start.Before(anchor):
		lost, err := pgRouteTelemetryLost(ctx, tx, now.Time)
		if err != nil {
			return report, err
		}
		if lost {
			unavailable = routeMonitorTelemetryUnavailable
		} else if err := pgReadRouteMonitorObservations(ctx, tx, account, &report); err != nil {
			return report, err
		}
	}
	routemonitor.Evaluate(&report, unavailable)
	return report, tx.Commit(ctx)
}
