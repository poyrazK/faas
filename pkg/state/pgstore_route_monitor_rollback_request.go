package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) createMonitorRollbackTx(ctx context.Context, tx pgx.Tx, acct, app, target, current, reason string, claim RouteMonitorRollbackClaim) (api.RollbackOperation, error) {
	q := sqlc.New()
	// beginAppTrafficMutation already owns the account/app mutation boundary.
	// Lock monitor state before deployment rows, matching monitor evaluation.
	row, err := q.LockRouteMonitorRollbackIncident(ctx, tx, sqlc.LockRouteMonitorRollbackIncidentParams{AccountID: acct, AppID: app})
	if err != nil {
		return api.RollbackOperation{}, mapErr(err)
	}
	i, err := pgReadRouteMonitorIncident(ctx, tx, acct, app, claim.IncidentID)
	if err != nil {
		return api.RollbackOperation{}, err
	}
	if !claimMatchesIncident(claim, i, app, target, current) {
		return api.RollbackOperation{}, ErrConflict
	}
	if i.Rollback.Status == "requested" {
		r, err := decodeCheckedRollback(q.ReadCheckedRollback(ctx, tx, sqlc.ReadCheckedRollbackParams{ID: mustPgUUID(i.Rollback.OperationID), AppID: mustPgUUID(app), AccountID: mustPgUUID(acct)}))
		if err == nil && (r.TargetDeploymentID != target || r.CurrentDeploymentID != current) {
			return r, ErrConflict
		}
		return r, err
	}
	config, err := pgRouteMonitorConfig(ctx, tx, acct, app)
	if err != nil {
		return api.RollbackOperation{}, err
	}
	if row.ActiveIncidentID != i.ID || row.LastDeploymentID != current || !resumableRouteMonitorClaim(config, i) {
		return api.RollbackOperation{}, ErrConflict
	}
	id := uuid.NewString()
	i, err = requestedMonitorRollback(i, id, time.Now().UTC())
	if err != nil {
		return api.RollbackOperation{}, err
	}
	r, err := s.createCheckedRollbackTx(ctx, tx, acct, app, target, current, reason, id)
	if err != nil {
		return r, err
	}
	if err := pgWriteRouteMonitorIncident(ctx, tx, acct, i); err != nil {
		return r, err
	}
	return r, nil
}
