package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentRouteBudgetStore interface {
	GetEnvironmentRouteBudgets(context.Context, string, string, string) (api.EnvironmentRouteBudgets, error)
	SetEnvironmentRouteBudgets(context.Context, string, string, string, api.SetEnvironmentRouteBudgetsRequest) (api.EnvironmentRouteBudgets, error)
}

type scopedBudgetRow struct {
	Inherit   bool                    `json:"inherit"`
	Enabled   bool                    `json:"enabled"`
	Revision  int64                   `json:"revision"`
	Routes    []api.RouteMonitorRoute `json:"routes"`
	UpdatedAt time.Time               `json:"updated_at"`
}

func pgEnvironmentBudgetConfig(ctx context.Context, db sqlc.DBTX, account, app, scope string, lock bool) (api.EnvironmentRouteBudgets, error) {
	out := api.EnvironmentRouteBudgets{AppID: app, Scope: scope, BudgetSource: "app_policy"}
	if scope != api.DefaultEnvScope {
		if !api.ValidProjectEnvironmentSlug(scope) {
			return out, ErrInvalidArgument
		}
		if lock {
			row, err := sqlc.New().LockEnvironmentBudgetIdentity(ctx, db, sqlc.LockEnvironmentBudgetIdentityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope})
			if err != nil {
				return out, routePolicyReadError(err)
			}
			out.EnvironmentID, out.ProjectID = row.EnvironmentID, row.ProjectID
		} else {
			row, err := sqlc.New().ReadEnvironmentBudgetIdentity(ctx, db, sqlc.ReadEnvironmentBudgetIdentityParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope})
			if err != nil {
				return out, routePolicyReadError(err)
			}
			out.EnvironmentID, out.ProjectID = row.EnvironmentID, row.ProjectID
		}
	}
	parent, err := pgRouteMonitorConfig(ctx, db, account, app)
	if err != nil {
		return out, err
	}
	parent.CustomerGroupBy, parent.OnViolation = "", ""
	out.AppRevision, out.Config = parent.Revision, parent
	raw, err := sqlc.New().ReadEnvironmentRouteBudgets(ctx, db, sqlc.ReadEnvironmentRouteBudgetsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, EnvironmentID: out.EnvironmentID, ProjectID: out.ProjectID})
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	var saved scopedBudgetRow
	if err := json.Unmarshal(raw, &saved); err != nil {
		return out, err
	}
	out.Revision = saved.Revision
	if !saved.Inherit {
		out.BudgetSource = "environment_policy"
		out.Config = api.RouteMonitorConfig{AppID: app, Enabled: saved.Enabled, Revision: saved.Revision, Routes: saved.Routes, UpdatedAt: &saved.UpdatedAt}
	} else if out.Config.UpdatedAt == nil || saved.UpdatedAt.After(*out.Config.UpdatedAt) {
		// Switching back to inheritance also requires fresh completed windows.
		out.Config.UpdatedAt = &saved.UpdatedAt
	}
	return out, nil
}

func (s *PgStore) GetEnvironmentRouteBudgets(ctx context.Context, account, app, scope string) (api.EnvironmentRouteBudgets, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.EnvironmentRouteBudgets{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out, err := pgEnvironmentBudgetConfig(ctx, tx, account, app, scope, false)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) SetEnvironmentRouteBudgets(ctx context.Context, account, app, scope string, req api.SetEnvironmentRouteBudgetsRequest) (api.EnvironmentRouteBudgets, error) {
	if routemonitor.ValidateEnvironmentBudgets(req) != nil || req.AppID != app {
		return api.EnvironmentRouteBudgets{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.EnvironmentRouteBudgets{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, account, app, true)
	if err != nil {
		return api.EnvironmentRouteBudgets{}, err
	}
	current, err := pgEnvironmentBudgetConfig(ctx, tx, account, app, scope, true)
	if err != nil {
		return current, err
	}
	if req.ProjectID != current.ProjectID || req.EnvironmentID != current.EnvironmentID {
		return current, ErrRouteHealthRevision
	}
	hash, err := api.EnvironmentRouteBudgetRequestHash(scope, req)
	if err != nil {
		return current, err
	}
	receipt, err := sqlc.New().ReadEnvironmentBudgetReceipt(ctx, tx, sqlc.ReadEnvironmentBudgetReceiptParams{AccountID: mustPgUUID(account), RequestID: mustPgUUID(req.RequestID)})
	if err == nil {
		if receipt.RequestHash != hash {
			return current, ErrConflict
		}
		var saved api.EnvironmentRouteBudgets
		if err := json.Unmarshal(receipt.Response, &saved); err != nil {
			return current, err
		}
		return saved, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return current, err
	}
	if current.Revision != *req.ExpectedRevision || current.AppRevision != *req.ExpectedAppRevision || current.Revision >= api.RouteRequirementsMaxRevision {
		return current, ErrRouteHealthRevision
	}
	if !owner.Account.MayDeploy() {
		return current, ErrRouteInvestigationPlan
	}
	if (req.Enabled || req.Inherit && current.Config.Enabled) && !owner.Account.Plan.DebugTelemetryEnabled() {
		return current, ErrRouteInvestigationPlan
	}
	routes, err := json.Marshal(req.Routes)
	if err != nil {
		return current, err
	}
	err = sqlc.New().WriteEnvironmentRouteBudgets(ctx, tx, sqlc.WriteEnvironmentRouteBudgetsParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), Scope: scope, ProjectID: current.ProjectID, EnvironmentID: current.EnvironmentID, Inherit: req.Inherit, Enabled: req.Enabled, Revision: current.Revision + 1, Routes: routes})
	if err != nil {
		return current, err
	}
	updated, err := pgEnvironmentBudgetConfig(ctx, tx, account, app, scope, false)
	if err != nil {
		return current, err
	}
	response, err := json.Marshal(updated)
	if err != nil {
		return current, err
	}
	if err := sqlc.New().WriteEnvironmentBudgetReceipt(ctx, tx, sqlc.WriteEnvironmentBudgetReceiptParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(app), RequestID: mustPgUUID(req.RequestID), RequestHash: hash, Response: response}); err != nil {
		return current, err
	}
	return updated, tx.Commit(ctx)
}
