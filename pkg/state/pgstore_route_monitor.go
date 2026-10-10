package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeimpact"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/sourcecontext"
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
func (s *PgStore) PreviewRouteMonitor(ctx context.Context, accountID, appID string, req api.PreviewRouteMonitorRequest) (api.RouteMonitorPreview, error) {
	return s.PreviewRouteMonitorWithCustomerDetails(ctx, accountID, appID, req, false)
}
func (s *PgStore) PreviewRouteMonitorWithCustomerDetails(ctx context.Context, accountID, appID string, req api.PreviewRouteMonitorRequest, details bool) (api.RouteMonitorPreview, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteMonitorPreview{}, fmt.Errorf("begin monitor preview: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	owner, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RouteMonitorPreview{}, err
	}
	current, err := pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return api.RouteMonitorPreview{}, err
	}
	if routemonitor.ValidatePreviewRequest(req, current.Revision) != nil {
		return api.RouteMonitorPreview{}, ErrInvalidArgument
	}
	configChanged := !current.Enabled || current.CustomerGroupBy != req.CustomerGroupBy || !routemonitor.RoutesEqual(current.Routes, req.Routes)
	// A preview uses the current production windows. Leaving UpdatedAt unset
	// avoids applying the saved intent's anchor to a different proposal. When
	// the proposal is identical, preserve the saved anchor because saving it is
	// a no-op.
	proposed := api.RouteMonitorConfig{
		CustomerGroupBy: req.CustomerGroupBy,
		AppID:           appID,
		Enabled:         true,
		Revision:        current.Revision,
		Routes:          routemonitor.CloneRoutes(req.Routes),
	}
	if !configChanged && current.UpdatedAt != nil {
		updatedAt := current.UpdatedAt.UTC()
		proposed.UpdatedAt = &updatedAt
	}
	now, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return api.RouteMonitorPreview{}, err
	}
	report, _, _, err := pgRouteMonitorReport(ctx, tx, owner, proposed, now.Time, nil, emptyRouteMonitorRecoveryState())
	if err != nil {
		return api.RouteMonitorPreview{}, err
	}
	preview := api.RouteMonitorPreview{
		CurrentRevision:                     current.Revision,
		PreviewOnly:                         true,
		ConfigChangeResetsObservationAnchor: configChanged,
		Report:                              routemonitor.ProjectReport(report, details),
	}
	if err := tx.Commit(ctx); err != nil {
		return api.RouteMonitorPreview{}, fmt.Errorf("commit monitor preview: %w", err)
	}
	return preview, nil
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
	if c.Enabled == req.Enabled && c.CustomerGroupBy == req.CustomerGroupBy && routemonitor.OnViolation(c.OnViolation) == routemonitor.OnViolation(req.OnViolation) && routemonitor.RoutesEqual(c.Routes, req.Routes) {
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
	if err := sqlc.New().WriteRouteMonitorConfig(ctx, tx, sqlc.WriteRouteMonitorConfigParams{AppID: appID, AccountID: accountID, Enabled: req.Enabled, Revision: c.Revision + 1, Routes: body, CustomerGroupBy: req.CustomerGroupBy, OnViolation: routemonitor.OnViolation(req.OnViolation)}); err != nil {
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
func pgRouteMonitorReport(ctx context.Context, db sqlc.DBTX, owner RoutePolicySnapshot, c api.RouteMonitorConfig, now time.Time, active *api.RouteMonitorIncident, recovery routeMonitorRecoveryState) (api.RouteMonitorReport, routeMonitorRecoveryState, *api.RouteMonitorDeploymentBaseline, error) {
	r := routemonitor.NewReport(c, now)
	var candidateBaseline *api.RouteMonitorDeploymentBaseline
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
			return r, recovery, nil, fmt.Errorf("read monitor serving deployment: %w", err)
		}
		if len(serving) != 1 {
			unavailable = "serving_deployment_ambiguous_or_missing"
		} else {
			d := serving[0]
			if d.TrafficPercent != 100 || d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps {
				unavailable = "serving_deployment_not_ready"
			} else {
				candidateBaseline = routeMonitorDeploymentBaseline(d)
				r.DeploymentID, r.CommitSHA = d.ID, d.CommitSha.String
				anchor := d.CreatedAt.Time
				anchors := []time.Time{d.CanaryStepStartedAt.Time, d.RolloutCompletedAt.Time}
				if c.UpdatedAt != nil {
					anchors = append(anchors, c.UpdatedAt.UTC())
				}
				for _, at := range anchors {
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
					return r, recovery, nil, err
				}
				for i := range r.Routes {
					for j, w := range health.Routes[i].Windows {
						r.Routes[i].Windows[j].Observed = w.Candidate
					}
				}
				if c.CustomerGroupBy != "" {
					if err := pgRouteMonitorCustomers(ctx, db, owner.Account.ID, d.ID, r, recovery, active); err != nil {
						return r, recovery, nil, err
					}
				}
			}
		}
	}
	routemonitor.Evaluate(&r, unavailable)
	if unavailable == "" && r.DeploymentID != "" {
		if err := pgPooledRouteMonitor(ctx, db, owner.Account.ID, &r); err != nil {
			return r, recovery, nil, err
		}
	}
	if c.CustomerGroupBy == "" {
		recovery = emptyRouteMonitorRecoveryState()
	} else if r.DeploymentID != "" {
		recovery = routeMonitorRecoveryStateFor(recovery, active, r)
		recovery, r = mergeRouteMonitorRecoveryState(recovery, r)
	}
	return r, recovery, candidateBaseline, nil
}

func routeMonitorDeploymentBaseline(d sqlc.RouteMonitorServingDeploymentsRow) *api.RouteMonitorDeploymentBaseline {
	baseline := &api.RouteMonitorDeploymentBaseline{DeploymentID: d.ID}
	if routeimpact.ValidCommit(d.CommitSha.String) {
		baseline.CommitSHA = strings.ToLower(d.CommitSha.String)
	}
	if repository, reference := routeimpact.RepositoryReference(d.SourceUrl.String); repository != "" && (reference == "" || routeimpact.ValidCommit(d.CommitSha.String) && strings.EqualFold(reference, d.CommitSha.String)) {
		baseline.Repository = repository
	}
	if root, err := sourcecontext.Normalize(d.SourceRoot.String); err == nil && (d.SourceRoot.String == "" || root == d.SourceRoot.String) {
		baseline.SourceRoot = root
	}
	return baseline
}

func decodeRouteMonitorDeploymentBaseline(raw string) (*api.RouteMonitorDeploymentBaseline, error) {
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	var baseline api.RouteMonitorDeploymentBaseline
	if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
		return nil, fmt.Errorf("decode route monitor healthy baseline: %w", err)
	}
	if baseline.DeploymentID == "" {
		return nil, nil
	}
	return &baseline, nil
}

func encodeRouteMonitorDeploymentBaseline(baseline *api.RouteMonitorDeploymentBaseline) ([]byte, error) {
	if baseline == nil {
		return []byte(`{}`), nil
	}
	body, err := json.Marshal(baseline)
	if err != nil {
		return nil, fmt.Errorf("encode route monitor healthy baseline: %w", err)
	}
	if len(body) > api.RouteMonitorHealthyBaselineMaxBytes {
		return nil, fmt.Errorf("route monitor healthy baseline exceeds %d bytes", api.RouteMonitorHealthyBaselineMaxBytes)
	}
	return body, nil
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
	r, _, _, err := pgRouteMonitorReport(ctx, tx, owner, c, now.Time, active, recovery)
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
	r, nextRecovery, candidateBaseline, err := pgRouteMonitorReport(ctx, tx, owner, c, now, activeIncident, recovery)
	if err != nil {
		return false, err
	}
	recovery = nextRecovery
	lastHealthyBaseline, err := decodeRouteMonitorDeploymentBaseline(row.LastHealthyDeployment)
	if err != nil {
		return false, err
	}
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
		incident := newRouteMonitorIncident(r, lastHealthyBaseline)
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
		routemonitor.AppendIncidentTimeline(&incident, r)
		closeRouteMonitorIncident(&incident, "recovered", now, &r)
		if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
			return false, err
		}
		if err := pgNotifyRouteMonitor(ctx, tx, owner, incident); err != nil {
			return false, err
		}
		activeID = ""
	} else if activeID != "" {
		incident, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, activeID)
		if err != nil {
			return false, err
		}
		previous := incident.Timeline[len(incident.Timeline)-1]
		escalation := routemonitor.IncidentTimelineEscalation(previous, routemonitor.IncidentTimelineEntry(r))
		if routemonitor.AppendIncidentTimeline(&incident, r) {
			if escalation != nil {
				detail, err := pgBuildRouteMonitorIncidentEscalation(ctx, tx, owner.Account.ID, owner.App.Slug, incident, r, previous, escalation)
				if err != nil {
					return false, err
				}
				if !routemonitor.AppendIncidentEscalation(&incident, detail) {
					return false, fmt.Errorf("could not append route monitor escalation detail")
				}
			}
			if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
				return false, err
			}
			if escalation != nil {
				if err := pgNotifyRouteMonitorEscalation(ctx, tx, owner, incident, r, escalation); err != nil {
					return false, err
				}
			}
		}
	}
	if r.Status == "healthy" && candidateBaseline != nil {
		lastHealthyBaseline = candidateBaseline
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
	baselineBody, err := encodeRouteMonitorDeploymentBaseline(lastHealthyBaseline)
	if err != nil {
		return false, err
	}
	if err := sqlc.New().WriteRouteMonitorState(ctx, tx, sqlc.WriteRouteMonitorStateParams{AppID: appID, AccountID: accountID, IncidentID: activeID, DeploymentID: last, NextCheckAt: NewPgtypeTime(now.Add(api.RouteMonitorEvaluationInterval)), CustomerRecoveryState: recoveryBody, LastHealthyDeployment: baselineBody}); err != nil {
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

// pgPooledRouteMonitor reads pooled_windows only for routes whose one-minute
// windows were sparse, then re-evaluates with unchanged budgets (ADR-953).
// One-minute windows and customer cohorts stay as evaluated.
func pgPooledRouteMonitor(ctx context.Context, db sqlc.DBTX, accountID string, r *api.RouteMonitorReport) error {
	windows, ok := routemonitor.PooledWindows(r.ObservationAnchor, r.CheckedAt)
	if !ok {
		return nil
	}
	gate := api.RouteHealthGate{Routes: []api.RouteHealthRoute{}}
	health := api.RouteHealthReport{AppID: r.AppID, DeploymentID: r.DeploymentID, StableDeploymentID: r.DeploymentID, CheckedAt: r.CheckedAt, Routes: []api.RouteHealthFinding{}}
	healthWindows := make([]api.RouteHealthWindowEvidence, 0, len(windows))
	for _, w := range windows {
		healthWindows = append(healthWindows, api.RouteHealthWindowEvidence{Start: w.Start, End: w.End})
	}
	targets := []int{}
	for i, f := range r.Routes {
		if !routemonitor.NeedsPooledEvidence(f) {
			continue
		}
		gate.Routes = append(gate.Routes, api.RouteHealthRoute{Method: f.Route.Method, Path: f.Route.Path, MaxP95MS: f.Route.MaxP95MS})
		health.Routes = append(health.Routes, api.RouteHealthFinding{Method: f.Route.Method, Path: f.Route.Path, MaxP95MS: f.Route.MaxP95MS, Windows: append([]api.RouteHealthWindowEvidence(nil), healthWindows...)})
		targets = append(targets, i)
	}
	if len(targets) == 0 {
		return nil
	}
	// Passing the same ID reads one population. Only the candidate counts are used.
	if err := pgRouteHealthObservationsInWindows(ctx, db, accountID, gate, &health, api.RouteHealthInvestigationSelection{}, healthWindows); err != nil {
		return err
	}
	for k, i := range targets {
		pooled := append([]api.RouteMonitorWindow(nil), windows...)
		for j, w := range health.Routes[k].Windows {
			pooled[j].Observed = w.Candidate
		}
		r.Routes[i].PooledWindows = pooled
	}
	routemonitor.Evaluate(r, "")
	return nil
}
