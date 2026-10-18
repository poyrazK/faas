package state

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type ScopedRouteBudgetReportStore interface {
	ReadScopedRouteBudgetReport(context.Context, string, string, string) (api.ScopedRouteBudgetReport, error)
}

type ScopedRouteMonitorStore interface {
	ReadScopedRouteMonitor(context.Context, string, string, string) (api.RouteMonitorReport, error)
}

// Evaluate current completed windows in one read-only snapshot. The shared app
// budgets may be overridden; customer grouping, stored reports and actions are not.
func (s *PgStore) ReadScopedRouteMonitor(ctx context.Context, account, app, scope string) (api.RouteMonitorReport, error) {
	out, err := s.ReadScopedRouteBudgetReport(ctx, account, app, scope)
	return out.Report, err
}

func (s *PgStore) ReadScopedRouteBudgetReport(ctx context.Context, account, app, scope string) (api.ScopedRouteBudgetReport, error) {
	if scope != api.DefaultEnvScope && !api.ValidProjectEnvironmentSlug(scope) {
		return api.ScopedRouteBudgetReport{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.ScopedRouteBudgetReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, account, app, false)
	if err != nil {
		return api.ScopedRouteBudgetReport{}, err
	}
	budgets, err := pgEnvironmentBudgetConfig(ctx, tx, account, app, scope, false)
	if err != nil {
		return api.ScopedRouteBudgetReport{}, err
	}
	config := budgets.Config
	now, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return api.ScopedRouteBudgetReport{}, err
	}
	report, _, _, err := pgRouteMonitorReportScope(ctx, tx, owner, config, now.Time, nil, emptyRouteMonitorRecoveryState(), scope)
	if err != nil {
		return api.ScopedRouteBudgetReport{}, err
	}
	return api.ScopedRouteBudgetReport{Scope: scope, ProjectID: budgets.ProjectID, EnvironmentID: budgets.EnvironmentID, BudgetSource: budgets.BudgetSource, BudgetRevision: budgets.Revision, AppBudgetRevision: budgets.AppRevision, Report: report}, tx.Commit(ctx)
}

func pgMonitorServingScope(ctx context.Context, db sqlc.DBTX, owner RoutePolicySnapshot, scope string) ([]sqlc.RouteMonitorServingDeploymentsRow, error) {
	if scope == "" {
		return sqlc.New().RouteMonitorServingDeployments(ctx, db, owner.App.ID)
	}
	rows, err := sqlc.New().ScopedRouteMonitorServingDeployments(ctx, db, sqlc.ScopedRouteMonitorServingDeploymentsParams{AppID: owner.App.ID, AccountID: owner.Account.ID, Scope: scope})
	if err != nil {
		return nil, err
	}
	out := make([]sqlc.RouteMonitorServingDeploymentsRow, len(rows))
	for i, row := range rows {
		out[i] = sqlc.RouteMonitorServingDeploymentsRow(row)
	}
	return out, nil
}
