package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RouteMonitorRollbackStore = (*PgStore)(nil)

// ClaimRouteMonitorRollback decides the active incident under the same
// account, app and monitor locks as evaluation (ADR-952). It never writes
// deployments; the claim only reserves the incident's single decision.
func (s *PgStore) ClaimRouteMonitorRollback(ctx context.Context, accountID, appID string) (RouteMonitorRollbackClaim, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, fmt.Errorf("begin route monitor rollback claim: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, true); err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	row, err := sqlc.New().LockRouteMonitorRollbackIncident(ctx, tx, sqlc.LockRouteMonitorRollbackIncidentParams{AppID: appID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return RouteMonitorRollbackClaim{}, false, nil
	}
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, fmt.Errorf("lock route monitor rollback: %w", err)
	}
	if row.ActiveIncidentID == "" {
		return RouteMonitorRollbackClaim{}, false, nil
	}
	c, err := pgRouteMonitorConfig(ctx, tx, accountID, appID)
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	incident, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, row.ActiveIncidentID)
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	if incident.DeploymentID != row.LastDeploymentID {
		return RouteMonitorRollbackClaim{}, false, nil
	}
	d, err := s.DeploymentByID(ctx, incident.DeploymentID)
	if errors.Is(err, ErrNotFound) {
		return RouteMonitorRollbackClaim{}, false, nil
	}
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	clock, err := sqlc.New().RouteHealthClock(ctx, tx)
	if err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	claim, claimed, changed := decideRouteMonitorRollback(c, &incident, d, clock.Time)
	if !changed {
		return RouteMonitorRollbackClaim{}, false, nil
	}
	if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
		return RouteMonitorRollbackClaim{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RouteMonitorRollbackClaim{}, false, fmt.Errorf("commit route monitor rollback claim: %w", err)
	}
	return claim, claimed, nil
}

// RecordRouteMonitorRollback stores the final outcome of a claimed decision.
// The incident may have recovered or been superseded since the claim.
func (s *PgStore) RecordRouteMonitorRollback(ctx context.Context, accountID, appID, incidentID string, outcome api.RouteMonitorIncidentRollback) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin route monitor rollback record: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := pgRouteMonitorOwner(ctx, tx, accountID, appID, true); err != nil {
		return err
	}
	if _, err := sqlc.New().LockRouteMonitorRollbackIncident(ctx, tx, sqlc.LockRouteMonitorRollbackIncidentParams{AppID: appID, AccountID: accountID}); err != nil {
		return fmt.Errorf("lock route monitor rollback: %w", routePolicyReadError(err))
	}
	incident, err := pgReadRouteMonitorIncident(ctx, tx, accountID, appID, incidentID)
	if err != nil {
		return err
	}
	if err := recordRouteMonitorRollback(&incident, outcome); err != nil {
		return err
	}
	if err := pgWriteRouteMonitorIncident(ctx, tx, accountID, incident); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
