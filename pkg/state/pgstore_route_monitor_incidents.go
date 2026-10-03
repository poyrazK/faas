package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgReadRouteMonitorIncident(ctx context.Context, db sqlc.DBTX, accountID, appID, id string) (api.RouteMonitorIncident, error) {
	body, err := sqlc.New().ReadRouteMonitorIncident(ctx, db, sqlc.ReadRouteMonitorIncidentParams{AccountID: accountID, AppID: appID, ID: id})
	if err != nil {
		return api.RouteMonitorIncident{}, routePolicyReadError(err)
	}
	var i api.RouteMonitorIncident
	if err := json.Unmarshal(body, &i); err != nil {
		return i, fmt.Errorf("decode route monitor incident: %w", err)
	}
	return i, nil
}
func pgWriteRouteMonitorIncident(ctx context.Context, db sqlc.DBTX, accountID string, i api.RouteMonitorIncident) error {
	body, err := encodeRouteMonitorIncident(i)
	if err != nil {
		return err
	}
	closed := time.Time{}
	if i.ClosedAt != nil {
		closed = *i.ClosedAt
	}
	if err := sqlc.New().WriteRouteMonitorIncident(ctx, db, sqlc.WriteRouteMonitorIncidentParams{ID: i.ID, AppID: i.AppID, AccountID: accountID, DeploymentID: i.DeploymentID, Revision: i.Revision, Status: i.Status, OpenedAt: NewPgtypeTime(i.OpenedAt), ClosedAt: NewPgtypeTime(closed), EncodedBytes: int64(len(body)), Entry: body}); err != nil {
		return fmt.Errorf("save route monitor incident: %w", err)
	}
	return nil
}
func pgSupersedeRouteMonitor(ctx context.Context, db sqlc.DBTX, accountID, appID, id string) error {
	i, err := pgReadRouteMonitorIncident(ctx, db, accountID, appID, id)
	if err != nil {
		return err
	}
	clock, err := sqlc.New().RouteHealthClock(ctx, db)
	if err != nil {
		return err
	}
	closeRouteMonitorIncident(&i, "superseded", clock.Time, nil)
	return pgWriteRouteMonitorIncident(ctx, db, accountID, i)
}
func pgPruneRouteMonitor(ctx context.Context, db sqlc.DBTX, appID string) error {
	if err := sqlc.New().PruneRouteMonitorIncidents(ctx, db, sqlc.PruneRouteMonitorIncidentsParams{AppID: appID, MaxEntries: api.RouteMonitorHistoryMaxEntries, MaxBytes: api.RouteMonitorHistoryMaxBytes}); err != nil {
		return fmt.Errorf("prune route monitor incidents: %w", err)
	}
	return nil
}
func pgNotifyRouteMonitor(ctx context.Context, db sqlc.DBTX, owner RoutePolicySnapshot, i api.RouteMonitorIncident) error {
	event, body, err := routeMonitorNotification(i, owner.App.Slug)
	if err != nil {
		return err
	}
	if err := sqlc.New().EnqueueRouteHealthNotification(ctx, db, sqlc.EnqueueRouteHealthNotificationParams{AppID: i.AppID, AccountID: owner.Account.ID, Event: string(event), DecisionID: i.ID, Payload: body}); err != nil {
		return fmt.Errorf("enqueue route monitor event: %w", err)
	}
	return nil
}
func (s *PgStore) monitorIncidentRead(ctx context.Context, accountID, appID string) (pgx.Tx, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin monitor incident read: %w", err)
	}
	owner, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, false)
	if err == nil && !owner.Account.Plan.DebugTelemetryEnabled() {
		err = ErrRouteInvestigationPlan
	}
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	return tx, nil
}
func (s *PgStore) GetRouteMonitorIncident(ctx context.Context, accountID, appID, id string) (api.RouteMonitorIncident, error) {
	tx, err := s.monitorIncidentRead(ctx, accountID, appID)
	if err != nil {
		return api.RouteMonitorIncident{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	i, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, id)
	if err != nil {
		return i, err
	}
	return i, tx.Commit(ctx)
}
func (s *PgStore) ListRouteMonitorIncidents(ctx context.Context, accountID, appID string, limit int, before string) (api.RouteMonitorIncidentPage, error) {
	out := api.RouteMonitorIncidentPage{AppID: appID, Incidents: []api.RouteMonitorIncident{}}
	if limit < 1 || limit > api.RouteMonitorMaxPage {
		return out, ErrInvalidArgument
	}
	tx, err := s.monitorIncidentRead(ctx, accountID, appID)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if before != "" {
		if _, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, before); err != nil {
			return out, err
		}
	}
	bodies, err := sqlc.New().ListRouteMonitorIncidents(ctx, tx, sqlc.ListRouteMonitorIncidentsParams{AccountID: accountID, AppID: appID, BeforeID: before, PageLimit: int32(limit + 1)})
	if err != nil {
		return out, fmt.Errorf("list route monitor incidents: %w", err)
	}
	for _, body := range bodies {
		var i api.RouteMonitorIncident
		if err := json.Unmarshal(body, &i); err != nil {
			return out, fmt.Errorf("decode route monitor history: %w", err)
		}
		out.Incidents = append(out.Incidents, i)
	}
	if len(out.Incidents) > limit {
		out.Incidents = out.Incidents[:limit]
		out.NextBefore = out.Incidents[limit-1].ID
	}
	return out, tx.Commit(ctx)
}
func pgRouteMonitorEvidence(ctx context.Context, db sqlc.DBTX, accountID, slug string, i *api.RouteMonitorIncident) error {
	report := i.OpeningReport
	for _, f := range report.Routes {
		for _, signal := range []string{"errors", "latency"} {
			if signal == "errors" && f.ErrorStatus != "violated" || signal == "latency" && f.LatencyStatus != "violated" {
				continue
			}
			if len(i.Evidence) >= api.RouteMonitorEvidenceRoutesLimit {
				i.EvidenceTruncated = true
				continue
			}
			evidence := api.RouteMonitorEvidence{Method: f.Route.Method, Path: f.Route.Path, Signal: signal, Windows: []api.RouteMonitorEvidenceWindow{}}
			out := api.RouteHealthInvestigation{Report: api.RouteHealthReport{AppID: report.AppID, DeploymentID: report.DeploymentID, StableDeploymentID: report.DeploymentID}, Selection: api.RouteHealthInvestigationSelection{Method: f.Route.Method, Path: f.Route.Path, Signal: signal}, Windows: []api.RouteHealthInvestigationWindow{}}
			for _, w := range f.Windows {
				out.Windows = append(out.Windows, api.RouteHealthInvestigationWindow{Start: w.Start, End: w.End})
			}
			if err := pgInvestigationExamples(ctx, db, accountID, slug, &out); err != nil {
				return err
			}
			for j := range out.Windows {
				out.Windows[j].Stable = api.RouteHealthInvestigationSide{Examples: []api.RouteHealthInvestigationExample{}}
			}
			if signal == "latency" {
				if err := pgInvestigationLatency(ctx, db, accountID, slug, &out); err != nil {
					return err
				}
			}
			for _, w := range out.Windows {
				evidence.Windows = append(evidence.Windows, api.RouteMonitorEvidenceWindow{Start: w.Start, End: w.End, Requests: w.Candidate, Diagnostics: w.Diagnostics})
			}
			i.Evidence = append(i.Evidence, evidence)
		}
	}
	return nil
}
