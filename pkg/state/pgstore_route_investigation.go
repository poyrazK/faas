package state

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteHealthInvestigationStore = (*PgStore)(nil)

func (s *PgStore) GetRouteHealthInvestigation(ctx context.Context, accountID, appID, deploymentID string, opts api.RouteHealthInvestigationOptions) (api.RouteHealthInvestigation, error) {
	if opts.Validate() != nil {
		return api.RouteHealthInvestigation{}, ErrInvalidArgument
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RouteHealthInvestigation{}, fmt.Errorf("begin route investigation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RouteHealthInvestigation{}, err
	}
	if !snapshot.Account.Plan.DebugTelemetryEnabled() {
		return api.RouteHealthInvestigation{}, ErrRouteInvestigationPlan
	}
	d, err := pgRouteHealthDeployment(ctx, tx, appID, deploymentID)
	if err != nil {
		return api.RouteHealthInvestigation{}, err
	}
	now, err := (&sqlc.Queries{}).RouteHealthClock(ctx, tx)
	if err != nil {
		return api.RouteHealthInvestigation{}, fmt.Errorf("read investigation clock: %w", err)
	}
	report, err := pgRouteHealthReport(ctx, tx, snapshot, d, now.Time)
	if err != nil {
		return api.RouteHealthInvestigation{}, err
	}
	if err := pgRouteClientErrors(ctx, tx, accountID, &report); err != nil {
		return api.RouteHealthInvestigation{}, err
	}
	investigation, err := pgBuildRouteInvestigation(ctx, tx, accountID, snapshot.App.Slug, report, opts)
	if err != nil {
		return investigation, err
	}
	if err := tx.Commit(ctx); err != nil {
		return investigation, fmt.Errorf("commit route investigation read: %w", err)
	}
	return investigation, nil
}

func pgBuildRouteInvestigation(ctx context.Context, tx sqlc.DBTX, accountID, slug string, report api.RouteHealthReport, opts api.RouteHealthInvestigationOptions) (api.RouteHealthInvestigation, error) {
	finding, err := routehealth.InvestigationFinding(report, opts)
	if err != nil {
		return api.RouteHealthInvestigation{}, ErrInvalidArgument
	}
	selection := opts.Selection()
	if selection.CustomerID != "" {
		if err := pgInvestigationCustomer(ctx, tx, accountID, report.AppID, selection); err != nil {
			return api.RouteHealthInvestigation{}, err
		}
		finding, err = pgInvestigationCustomerFinding(ctx, tx, accountID, report, finding, selection)
		if err != nil {
			return api.RouteHealthInvestigation{}, err
		}
	}
	out := routehealth.NewInvestigation(report, finding, selection)
	if report.StableDeploymentID == "" {
		return out, nil
	}
	if err := pgInvestigationExamples(ctx, tx, accountID, slug, &out); err != nil {
		return out, err
	}
	if out.Selection.Signal == "latency" {
		if err := pgInvestigationLatency(ctx, tx, accountID, slug, &out); err != nil {
			return out, err
		}
	}
	out.EvidenceStatus = "observed"
	return out, nil
}

func pgInvestigationCustomer(ctx context.Context, db sqlc.DBTX, accountID, appID string, selection api.RouteHealthInvestigationSelection) error {
	owned, err := (&sqlc.Queries{}).RouteHealthInvestigationCustomerExists(ctx, db, sqlc.RouteHealthInvestigationCustomerExistsParams{AccountID: accountID, AppID: appID, CustomerID: selection.CustomerID, CustomerGroupBy: selection.CustomerGroupBy})
	if err != nil {
		return fmt.Errorf("resolve investigation customer: %w", err)
	}
	if !owned {
		return ErrNotFound
	}
	return nil
}

func pgInvestigationCustomerFinding(ctx context.Context, db sqlc.DBTX, accountID string, report api.RouteHealthReport, f api.RouteHealthFinding, selection api.RouteHealthInvestigationSelection) (api.RouteHealthFinding, error) {
	target := report
	target.Routes = []api.RouteHealthFinding{{Method: f.Method, Path: f.Path, CheckLatency: f.CheckLatency, MaxP95MS: f.MaxP95MS, WatchStatuses: f.WatchStatuses, Windows: routehealth.Windows(report.CheckedAt)}}
	unavailable := ""
	if report.StableDeploymentID == "" {
		unavailable = report.Reason
	} else {
		g := api.RouteHealthGate{Routes: []api.RouteHealthRoute{{Method: f.Method, Path: f.Path, CheckLatency: f.CheckLatency, MaxP95MS: f.MaxP95MS}}}
		if err := pgRouteHealthObservationsForCustomer(ctx, db, accountID, g, &target, selection); err != nil {
			return f, err
		}
	}
	routehealth.Evaluate(&target, report.ObservationAnchor, unavailable)
	if err := pgRouteClientErrorsForCustomer(ctx, db, accountID, &target, selection); err != nil {
		return f, err
	}
	return target.Routes[0], nil
}

func pgInvestigationExamples(ctx context.Context, db sqlc.DBTX, accountID, slug string, out *api.RouteHealthInvestigation) error {
	encoded, err := json.Marshal(out.Windows)
	if err != nil {
		return fmt.Errorf("encode investigation windows: %w", err)
	}
	minimum, maximum := int32(out.Selection.StatusCode), int32(out.Selection.StatusCode)
	if minimum == 0 {
		minimum, maximum = 500, 599
	}
	body, err := (&sqlc.Queries{}).RouteHealthInvestigationExamples(ctx, db, sqlc.RouteHealthInvestigationExamplesParams{AccountID: accountID, AppID: out.Report.AppID, CandidateID: out.Report.DeploymentID, StableID: out.Report.StableDeploymentID, Method: out.Selection.Method, Path: out.Selection.Path, CustomerID: out.Selection.CustomerID, CustomerGroupBy: out.Selection.CustomerGroupBy, Windows: encoded, StatusMin: minimum, StatusMax: maximum, ExampleLimit: api.RouteHealthInvestigationExamplesLimit, Latency: out.Selection.Signal == "latency"})
	if err != nil {
		return fmt.Errorf("read investigation examples: %w", err)
	}
	if err := json.Unmarshal(body, &out.Windows); err != nil {
		return fmt.Errorf("decode investigation examples: %w", err)
	}
	for i := range out.Windows {
		for _, side := range []*api.RouteHealthInvestigationSide{&out.Windows[i].Candidate, &out.Windows[i].Stable} {
			for j := range side.Examples {
				e := &side.Examples[j]
				e.EvidencePath = "/v1/apps/" + url.PathEscape(slug) + "/debug/requests/" + e.TelemetryID + "/evidence"
			}
		}
	}
	return nil
}
