package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteHealthStore = (*PgStore)(nil)

func pgRouteHealthGate(ctx context.Context, db sqlc.DBTX, accountID, appID string) (api.RouteHealthGate, error) {
	body, err := (&sqlc.Queries{}).ReadRouteHealthGate(ctx, db, sqlc.ReadRouteHealthGateParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return api.RouteHealthGate{}, routePolicyReadError(err)
	}
	var g api.RouteHealthGate
	if err := json.Unmarshal(body, &g); err != nil {
		return g, fmt.Errorf("decode route health gate: %w", err)
	}
	return g, nil
}
func (s *PgStore) GetRouteHealthGate(ctx context.Context, accountID, appID string) (api.RouteHealthGate, error) {
	return pgRouteHealthGate(ctx, s.pool, accountID, appID)
}
func (s *PgStore) SetRouteHealthGate(ctx context.Context, accountID, appID string, req api.SetRouteHealthGateRequest) (api.RouteHealthGate, error) {
	if err := routehealth.Validate(req); err != nil {
		return api.RouteHealthGate{}, ErrInvalidArgument
	}
	req.OnRegression = routehealth.RegressionAction(req.OnRegression)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.RouteHealthGate{}, fmt.Errorf("begin route health update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	if err != nil {
		return api.RouteHealthGate{}, err
	}
	g, err := pgRouteHealthGate(ctx, tx, accountID, appID)
	if err != nil {
		return g, err
	}
	if g.Revision != *req.ExpectedRevision {
		return g, ErrRouteHealthRevision
	}
	if req.Mode == "enforce" && (!snapshot.Account.Plan.TrafficSplitAllowed() || !snapshot.Account.Plan.DebugTelemetryEnabled()) {
		return g, ErrRouteHealthPlan
	}
	// The first explicit save records intent even when it matches the default,
	// so an empty selector list opts out of default seeding (ADR-951).
	if g.Revision > 0 && g.Mode == req.Mode && g.OnRegression == req.OnRegression && routehealth.RoutesEqual(g.Routes, req.Routes) {
		return g, tx.Commit(ctx)
	}
	if g.Revision >= api.RouteRequirementsMaxRevision {
		return g, ErrRouteHealthRevision
	}
	routes := req.Routes
	if routes == nil {
		routes = []api.RouteHealthRoute{}
	}
	body, err := json.Marshal(routes)
	if err != nil {
		return g, fmt.Errorf("encode route health routes: %w", err)
	}
	if err := (&sqlc.Queries{}).WriteRouteHealthGate(ctx, tx, sqlc.WriteRouteHealthGateParams{AppID: appID, AccountID: accountID, Mode: req.Mode, OnRegression: req.OnRegression, Revision: g.Revision + 1, Routes: body}); err != nil {
		return g, fmt.Errorf("write route health gate: %w", err)
	}
	g, err = pgRouteHealthGate(ctx, tx, accountID, appID)
	if err != nil {
		return g, err
	}
	return g, tx.Commit(ctx)
}
func pgRouteHealthDeployment(ctx context.Context, db sqlc.DBTX, appID, deploymentID string) (Deployment, error) {
	r, err := (&sqlc.Queries{}).ReadRouteHealthDeployment(ctx, db, sqlc.ReadRouteHealthDeploymentParams{AppID: appID, DeploymentID: deploymentID})
	if err != nil {
		return Deployment{}, routePolicyReadError(err)
	}
	d := Deployment{ID: r.ID, AppID: r.AppID, CommitSHA: r.CommitSha.String, Status: DeploymentStatus(r.Status), TrafficPercent: int(r.TrafficPercent), CanaryStep: int(r.CanaryStep), CanaryTotalSteps: int(r.CanaryTotalSteps), Scope: r.Scope}
	if r.CanaryStepStartedAt.Valid {
		d.CanaryStepStartedAt = &r.CanaryStepStartedAt.Time
	}
	return d, nil
}
func pgRouteHealthReport(ctx context.Context, db sqlc.DBTX, snapshot RoutePolicySnapshot, d Deployment, now time.Time) (api.RouteHealthReport, error) {
	g, err := pgRouteHealthGate(ctx, db, snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return api.RouteHealthReport{}, err
	}
	report, anchor := newRouteHealthReport(g, d, now)
	unavailable := ""
	if !snapshot.Account.Plan.DebugTelemetryEnabled() || g.Mode == "enforce" && !snapshot.Account.Plan.TrafficSplitAllowed() {
		unavailable = "telemetry_not_entitled"
	}
	if d.Status != DeployLive || d.TrafficPercent <= 0 || d.CanaryTotalSteps <= 0 || d.CanaryStep >= d.CanaryTotalSteps {
		unavailable = "candidate_not_in_flight"
	}
	if len(g.Routes) > 0 && unavailable == "" {
		ids, err := (&sqlc.Queries{}).RouteHealthStableIDs(ctx, db, sqlc.RouteHealthStableIDsParams{AppID: snapshot.App.ID, DeploymentID: d.ID})
		if err != nil {
			return report, fmt.Errorf("read route health stable deployment: %w", err)
		}
		if len(ids) != 1 {
			unavailable = "stable_deployment_ambiguous_or_missing"
		} else {
			stable, err := pgRouteHealthDeployment(ctx, db, snapshot.App.ID, ids[0])
			if err != nil {
				return report, err
			}
			report.StableDeploymentID, report.StableCommitSHA = stable.ID, stable.CommitSHA
			if err := pgRouteHealthObservations(ctx, db, snapshot.Account.ID, g, &report); err != nil {
				return report, err
			}
		}
	}
	routehealth.Evaluate(&report, anchor, unavailable)
	if unavailable == "" && report.StableDeploymentID != "" {
		if err := pgPooledRouteHealth(ctx, db, snapshot.Account.ID, g, &report, anchor); err != nil {
			return report, err
		}
	}
	return report, nil
}

// pgPooledRouteHealth reads pooled_windows only for routes whose one-minute
// windows lacked requests, then re-evaluates with unchanged thresholds
// (ADR-953). One-minute windows stay in every finding.
func pgPooledRouteHealth(ctx context.Context, db sqlc.DBTX, accountID string, g api.RouteHealthGate, report *api.RouteHealthReport, anchor *time.Time) error {
	windows, ok := routehealth.PooledWindows(anchor, report.CheckedAt)
	if !ok {
		return nil
	}
	selected := api.RouteHealthGate{Routes: []api.RouteHealthRoute{}}
	pooled := *report
	pooled.Routes = []api.RouteHealthFinding{}
	targets := []int{}
	for i, f := range report.Routes {
		if i >= len(g.Routes) || !routehealth.NeedsPooledEvidence(f) {
			continue
		}
		selected.Routes = append(selected.Routes, g.Routes[i])
		pooled.Routes = append(pooled.Routes, api.RouteHealthFinding{Method: f.Method, Path: f.Path, Windows: append([]api.RouteHealthWindowEvidence(nil), windows...)})
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return nil
	}
	if err := pgRouteHealthObservationsInWindows(ctx, db, accountID, selected, &pooled, api.RouteHealthInvestigationSelection{}, windows); err != nil {
		return err
	}
	for k, i := range targets {
		report.Routes[i].PooledWindows = pooled.Routes[k].Windows
	}
	routehealth.Evaluate(report, anchor, "")
	// Probes count only for opted-in routes that pooling left sparse (ADR-954).
	if err := pgSyntheticRouteHealth(ctx, db, accountID, g, report, anchor, windows); err != nil {
		return err
	}
	routehealth.Evaluate(report, anchor, "")
	return nil
}
func pgRouteHealthObservations(ctx context.Context, db sqlc.DBTX, accountID string, g api.RouteHealthGate, report *api.RouteHealthReport) error {
	return pgRouteHealthObservationsForCustomer(ctx, db, accountID, g, report, api.RouteHealthInvestigationSelection{})
}

func pgRouteHealthObservationsForCustomer(ctx context.Context, db sqlc.DBTX, accountID string, g api.RouteHealthGate, report *api.RouteHealthReport, selection api.RouteHealthInvestigationSelection) error {
	return pgRouteHealthObservationsInWindows(ctx, db, accountID, g, report, selection, routehealth.Windows(report.CheckedAt))
}

func pgRouteHealthObservationsInWindows(ctx context.Context, db sqlc.DBTX, accountID string, g api.RouteHealthGate, report *api.RouteHealthReport, selection api.RouteHealthInvestigationSelection, windows []api.RouteHealthWindowEvidence) error {
	routesJSON, err := json.Marshal(g.Routes)
	if err != nil {
		return fmt.Errorf("encode selected routes: %w", err)
	}
	windowsJSON, err := json.Marshal(windows)
	if err != nil {
		return fmt.Errorf("encode observation windows: %w", err)
	}
	body, err := (&sqlc.Queries{}).RouteHealthObservation(ctx, db, sqlc.RouteHealthObservationParams{AppID: report.AppID, AccountID: accountID, CandidateID: report.DeploymentID, StableID: report.StableDeploymentID, Routes: routesJSON, Windows: windowsJSON, LatencyQuantile: api.RouteHealthLatencyQuantile, Since: NewPgtypeTime(windows[0].Start), Until: NewPgtypeTime(windows[len(windows)-1].End), CustomerID: selection.CustomerID, CustomerGroupBy: selection.CustomerGroupBy})
	if err != nil {
		return fmt.Errorf("read route health observations: %w", err)
	}
	var rows []struct {
		Method string `json:"method"`
		Path   string `json:"path"`
		api.RouteHealthWindowEvidence
	}
	if err := json.Unmarshal(body, &rows); err != nil {
		return fmt.Errorf("decode route health observations: %w", err)
	}
	for i := range report.Routes {
		for j := range report.Routes[i].Windows {
			for _, row := range rows {
				if row.Method == report.Routes[i].Method && row.Path == report.Routes[i].Path && row.Start.Equal(report.Routes[i].Windows[j].Start) {
					report.Routes[i].Windows[j] = row.RouteHealthWindowEvidence
				}
			}
		}
	}
	return nil
}
func (s *PgStore) GetRouteHealthReport(ctx context.Context, accountID, appID, deploymentID string) (api.RouteHealthReport, error) {
	return s.getRouteHealthReport(ctx, accountID, appID, deploymentID, "", false)
}
func (s *PgStore) getRouteHealthReport(ctx context.Context, accountID, appID, deploymentID, groupBy string, details bool) (api.RouteHealthReport, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return api.RouteHealthReport{}, fmt.Errorf("begin route health report: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RouteHealthReport{}, err
	}
	d, err := pgRouteHealthDeployment(ctx, tx, appID, deploymentID)
	if err != nil {
		return api.RouteHealthReport{}, err
	}
	now, err := (&sqlc.Queries{}).RouteHealthClock(ctx, tx)
	if err != nil {
		return api.RouteHealthReport{}, fmt.Errorf("read route health clock: %w", err)
	}
	report, err := pgRouteHealthReport(ctx, tx, snapshot, d, now.Time)
	if err != nil {
		return report, err
	}
	if err := pgRouteClientErrors(ctx, tx, accountID, &report); err != nil {
		return report, err
	}
	if groupBy != "" {
		if err := pgRouteCustomerHealth(ctx, tx, accountID, &report, groupBy, details); err != nil {
			return report, err
		}
	}
	return report, tx.Commit(ctx)
}
func pgCheckRouteHealth(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, d Deployment, now time.Time, params CanaryAdvanceParams) error {
	report, err := pgRouteHealthReport(ctx, tx, snapshot, d, now)
	if err != nil {
		return err
	}
	entry, err := pgRecordRouteHealthHistory(ctx, tx, snapshot.Account.ID, report, d, params)
	if err != nil {
		return err
	}
	if err := pgRouteHealthNotification(ctx, tx, snapshot, entry); err != nil {
		return err
	}
	if entry.ID != "" {
		report = entry.Report
	}
	return requireRouteHealth(report, params.RouteHealthDecision, entry.ID)
}
