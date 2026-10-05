package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteMonitorStore = (*PgStore)(nil)
var _ RouteMonitorWorkerStore = (*PgStore)(nil)

func pgRouteMonitorConfig(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.RouteMonitorConfig, error) {
	body, err := sqlc.New().ReadRouteMonitorConfig(ctx, db, sqlc.ReadRouteMonitorConfigParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.RouteMonitorConfig{}, routePolicyReadError(err)
	}
	var c api.RouteMonitorConfig
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return c, fmt.Errorf("decode route monitor config: %w", err)
	}
	return c, nil
}

// Monitor work needs only account and app locks, in the same order as intent
// writes. It never locks edge rules or writes deployments/instances.
func pgRouteMonitorOwner(ctx context.Context, tx pgx.Tx, accountID, appID string, lock bool) (RoutePolicySnapshot, error) {
	q := sqlc.New()
	var a, b []byte
	var err error
	if lock {
		a, err = q.LockRoutePolicyAccount(ctx, tx, accountID)
	} else {
		a, err = q.ReadRoutePolicyAccount(ctx, tx, accountID)
	}
	if err != nil {
		return RoutePolicySnapshot{}, routePolicyReadError(err)
	}
	if lock {
		b, err = q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AccountID: accountID, AppID: appID})
	} else {
		b, err = q.ReadRoutePolicyApp(ctx, tx, sqlc.ReadRoutePolicyAppParams{AccountID: accountID, AppID: appID})
	}
	if err != nil {
		return RoutePolicySnapshot{}, routePolicyReadError(err)
	}
	var owner RoutePolicySnapshot
	if err := json.Unmarshal(a, &owner.Account); err != nil {
		return owner, fmt.Errorf("decode monitor account: %w", err)
	}
	if err := json.Unmarshal(b, &owner.App); err != nil {
		return owner, fmt.Errorf("decode monitor app: %w", err)
	}
	return owner, nil
}
func (s *PgStore) GetRouteMonitor(ctx context.Context, accountID, appID string) (api.RouteMonitorConfig, error) {
	return pgRouteMonitorConfig(ctx, s.pool, accountID, appID)
}
func (s *PgStore) SetRouteMonitor(ctx context.Context, accountID, appID string, req api.SetRouteMonitorRequest) (api.RouteMonitorConfig, error) {
	if routemonitor.Validate(req) != nil {
		return api.RouteMonitorConfig{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.RouteMonitorConfig{}, fmt.Errorf("begin monitor update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, true)
	if err != nil {
		return api.RouteMonitorConfig{}, err
	}
	c, err := pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return c, err
	}
	if c.Revision != *req.ExpectedRevision {
		return c, ErrRouteHealthRevision
	}
	if req.Enabled && (!owner.Account.Plan.DebugTelemetryEnabled() || !owner.Account.MayDeploy()) {
		return c, ErrRouteInvestigationPlan
	}
	if c.Enabled == req.Enabled && c.CustomerGroupBy == req.CustomerGroupBy && routemonitor.RoutesEqual(c.Routes, req.Routes) {
		return c, tx.Commit(ctx)
	}
	if c.Revision >= api.RouteRequirementsMaxRevision {
		return c, ErrRouteHealthRevision
	}
	if c.Revision > 0 {
		row, err := sqlc.New().LockRouteMonitor(ctx, tx, sqlc.LockRouteMonitorParams{AccountID: accountID, AppID: appID})
		if err != nil {
			return c, err
		}
		if row.ActiveIncidentID != "" {
			if err := pgSupersedeRouteMonitor(ctx, tx, accountID, appID, row.ActiveIncidentID); err != nil {
				return c, err
			}
		}
	}
	body, err := json.Marshal(req.Routes)
	if err != nil {
		return c, fmt.Errorf("encode monitor routes: %w", err)
	}
	if err := sqlc.New().WriteRouteMonitorConfig(ctx, tx, sqlc.WriteRouteMonitorConfigParams{AppID: appID, AccountID: accountID, Enabled: req.Enabled, Revision: c.Revision + 1, Routes: body, CustomerGroupBy: req.CustomerGroupBy}); err != nil {
		return c, fmt.Errorf("write monitor config: %w", err)
	}
	c, err = pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return c, err
	}
	if err := pgPruneRouteMonitor(ctx, tx, appID); err != nil {
		return c, err
	}
	return c, tx.Commit(ctx)
}
func pgRouteMonitorReport(ctx context.Context, db sqlc.DBTX, owner RoutePolicySnapshot, c api.RouteMonitorConfig, now time.Time, active *api.RouteMonitorIncident, recovery routeMonitorRecoveryState) (api.RouteMonitorReport, routeMonitorRecoveryState, error) {
	r := routemonitor.NewReport(c, now)
	unavailable := ""
	switch {
	case !c.Enabled:
		unavailable = "monitor_disabled"
	case !owner.Account.Plan.DebugTelemetryEnabled():
		unavailable = "telemetry_not_entitled"
	case !owner.Account.MayDeploy():
		unavailable = "account_ineligible"
	}
	if unavailable == "" {
		serving, err := sqlc.New().RouteMonitorServingDeployments(ctx, db, owner.App.ID)
		if err != nil {
			return r, recovery, fmt.Errorf("read monitor serving deployment: %w", err)
		}
		if len(serving) != 1 {
			unavailable = "serving_deployment_ambiguous_or_missing"
		} else {
			d := serving[0]
			if d.TrafficPercent != 100 || d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps {
				unavailable = "serving_deployment_not_ready"
			} else {
				r.DeploymentID, r.CommitSHA = d.ID, d.CommitSha.String
				anchor := d.CreatedAt.Time
				for _, at := range []time.Time{c.UpdatedAt.UTC(), d.CanaryStepStartedAt.Time, d.RolloutCompletedAt.Time} {
					if at.After(anchor) {
						anchor = at
					}
				}
				r.ObservationAnchor = &anchor
				health := api.RouteHealthReport{AppID: r.AppID, DeploymentID: d.ID, StableDeploymentID: d.ID, CheckedAt: now, Routes: []api.RouteHealthFinding{}}
				gate := api.RouteHealthGate{Routes: []api.RouteHealthRoute{}}
				for _, f := range r.Routes {
					gate.Routes = append(gate.Routes, api.RouteHealthRoute{Method: f.Route.Method, Path: f.Route.Path, MaxP95MS: f.Route.MaxP95MS})
					health.Routes = append(health.Routes, api.RouteHealthFinding{Method: f.Route.Method, Path: f.Route.Path, Windows: routehealth.Windows(now)})
				}
				// Passing the same ID reads one population. Only the candidate counts are used.
				if err := pgRouteHealthObservations(ctx, db, owner.Account.ID, gate, &health); err != nil {
					return r, recovery, err
				}
				for i := range r.Routes {
					for j, w := range health.Routes[i].Windows {
						r.Routes[i].Windows[j].Observed = w.Candidate
					}
				}
				if c.CustomerGroupBy != "" {
					if err := pgRouteMonitorCustomers(ctx, db, owner.Account.ID, d.ID, r, recovery, active); err != nil {
						return r, recovery, err
					}
				}
			}
		}
	}
	routemonitor.Evaluate(&r, unavailable)
	if c.CustomerGroupBy == "" {
		recovery = emptyRouteMonitorRecoveryState()
	} else if r.DeploymentID != "" {
		recovery = routeMonitorRecoveryStateFor(recovery, active, r)
		recovery, r = mergeRouteMonitorRecoveryState(recovery, r)
	}
	return r, recovery, nil
}
func (s *PgStore) GetRouteMonitorReport(ctx context.Context, accountID, appID string) (api.RouteMonitorReport, error) {
	return s.GetRouteMonitorReportWithCustomerDetails(ctx, accountID, appID, false)
}
func (s *PgStore) GetRouteMonitorReportWithCustomerDetails(ctx context.Context, accountID, appID string, details bool) (api.RouteMonitorReport, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteMonitorReport{}, fmt.Errorf("begin monitor read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RouteMonitorReport{}, err
	}
	c, err := pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return api.RouteMonitorReport{}, err
	}
	now, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return api.RouteMonitorReport{}, err
	}
	var active *api.RouteMonitorIncident
	recovery := emptyRouteMonitorRecoveryState()
	if c.CustomerGroupBy != "" {
		active, err = pgActiveRouteMonitorIncident(ctx, tx, accountID, appID)
		if err != nil {
			return api.RouteMonitorReport{}, err
		}
		if active != nil && active.Status == "open" && active.Revision == c.Revision && active.OpeningReport.CustomerGroupBy == c.CustomerGroupBy {
			recovery, err = pgReadRouteMonitorRecoveryState(ctx, tx, accountID, appID)
			if err != nil {
				return api.RouteMonitorReport{}, err
			}
		}
	}
	r, _, err := pgRouteMonitorReport(ctx, tx, owner, c, now.Time, active, recovery)
	if err != nil {
		return r, err
	}
	if err := tx.Commit(ctx); err != nil {
		return r, err
	}
	return routemonitor.ProjectReport(r, details), nil
}
func (s *PgStore) ListDueRouteMonitors(ctx context.Context) ([]RouteMonitorTarget, error) {
	rows, err := sqlc.New().ListDueRouteMonitors(ctx, s.pool, int32(api.RouteMonitorBatchSize))
	if err != nil {
		return nil, fmt.Errorf("list due route monitors: %w", err)
	}
	out := make([]RouteMonitorTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, RouteMonitorTarget{AppID: r.AppID, AccountID: r.AccountID, Revision: r.Revision, DueAt: r.NextCheckAt.Time})
	}
	return out, nil
}
func (s *PgStore) EvaluateRouteMonitor(ctx context.Context, accountID, appID string) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return false, fmt.Errorf("begin monitor evaluation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, true)
	if err != nil {
		return false, err
	}
	row, err := sqlc.New().LockRouteMonitor(ctx, tx, sqlc.LockRouteMonitorParams{AppID: appID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("lock monitor: %w", err)
	}
	c, err := pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return false, err
	}
	clock, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return false, err
	}
	now := clock.Time
	if !c.Enabled || row.NextCheckAt.Time.After(now) {
		return false, nil
	}
	var activeIncident *api.RouteMonitorIncident
	recovery := emptyRouteMonitorRecoveryState()
	if c.CustomerGroupBy != "" && row.ActiveIncidentID != "" {
		activeIncidentValue, readErr := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, row.ActiveIncidentID)
		err = readErr
		if err != nil {
			return false, err
		}
		activeIncident = &activeIncidentValue
		if activeIncident.Status == "open" && activeIncident.Revision == c.Revision && activeIncident.OpeningReport.CustomerGroupBy == c.CustomerGroupBy {
			recovery, err = pgReadRouteMonitorRecoveryState(ctx, tx, accountID, appID)
			if err != nil {
				return false, err
			}
		}
	}
	r, nextRecovery, err := pgRouteMonitorReport(ctx, tx, owner, c, now, activeIncident, recovery)
	if err != nil {
		return false, err
	}
	recovery = nextRecovery
	activeID, last := row.ActiveIncidentID, row.LastDeploymentID
	if r.DeploymentID != "" && r.DeploymentID != last {
		if activeID != "" {
			if err := pgSupersedeRouteMonitor(ctx, tx, accountID, appID, activeID); err != nil {
				return false, err
			}
		}
		activeID, last = "", r.DeploymentID
	}
	if r.Status == "violated" && activeID == "" {
		incident := newRouteMonitorIncident(r)
		if err := pgRouteMonitorEvidence(ctx, tx, owner.Account.ID, owner.App.Slug, &incident); err != nil {
			return false, err
		}
		if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
			return false, err
		}
		if err := pgNotifyRouteMonitor(ctx, tx, owner, incident); err != nil {
			return false, err
		}
		activeID = incident.ID
	} else if r.Status == "healthy" && activeID != "" {
		incident, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, activeID)
		if err != nil {
			return false, err
		}
		closeRouteMonitorIncident(&incident, "recovered", now, &r)
		if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
			return false, err
		}
		if err := pgNotifyRouteMonitor(ctx, tx, owner, incident); err != nil {
			return false, err
		}
		activeID = ""
	}
	if activeID == "" || c.CustomerGroupBy == "" {
		recovery = emptyRouteMonitorRecoveryState()
	}
	recoveryBody, err := json.Marshal(recovery)
	if err != nil {
		return false, fmt.Errorf("encode route monitor recovery customers: %w", err)
	}
	if len(recoveryBody) > api.RouteMonitorRecoveryStateMaxBytes {
		return false, fmt.Errorf("route monitor recovery customers exceed %d bytes", api.RouteMonitorRecoveryStateMaxBytes)
	}
	if err := sqlc.New().WriteRouteMonitorState(ctx, tx, sqlc.WriteRouteMonitorStateParams{AppID: appID, AccountID: accountID, IncidentID: activeID, DeploymentID: last, NextCheckAt: NewPgtypeTime(now.Add(api.RouteMonitorEvaluationInterval)), CustomerRecoveryState: recoveryBody}); err != nil {
		return false, fmt.Errorf("schedule monitor evaluation: %w", err)
	}
	if err := pgPruneRouteMonitor(ctx, tx, appID); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func (s *PgStore) DeferRouteMonitor(ctx context.Context, t RouteMonitorTarget) error {
	err := sqlc.New().DeferRouteMonitor(ctx, s.pool, sqlc.DeferRouteMonitorParams{AppID: t.AppID, AccountID: t.AccountID, Revision: t.Revision, PreviousDueAt: NewPgtypeTime(t.DueAt), NextCheckAt: NewPgtypeTime(time.Now().UTC().Add(api.RouteMonitorEvaluationInterval))})
	if err != nil {
		return fmt.Errorf("defer failed route monitor: %w", err)
	}
	return nil
}
