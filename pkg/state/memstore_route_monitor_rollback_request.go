package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) createMonitorRollbackLocked(ctx context.Context, acct, app, target, current, reason string, claim RouteMonitorRollbackClaim) (api.RollbackOperation, error) {
	if owner, ok := m.apps[app]; !ok || owner.AccountID != acct || owner.Status == AppDeleted {
		return api.RollbackOperation{}, ErrNotFound
	}
	for j, i := range m.routeMonitorIncidents[app] {
		if i.ID != claim.IncidentID {
			continue
		}
		if !claimMatchesIncident(claim, i, app, target, current) {
			return api.RollbackOperation{}, ErrConflict
		}
		if i.Rollback.Status == "requested" {
			r, ok := m.checkedRollbacks[i.Rollback.OperationID]
			if !ok || r.AppID != app || r.TargetDeploymentID != target || r.CurrentDeploymentID != current {
				return api.RollbackOperation{}, ErrConflict
			}
			return cloneRollback(r), nil
		}
		config, err := m.routeMonitorConfigLocked(acct, app)
		if err != nil {
			return api.RollbackOperation{}, err
		}
		if !resumableRouteMonitorClaim(config, i) {
			return api.RollbackOperation{}, ErrConflict
		}
		id := uuid.NewString()
		requested, err := requestedMonitorRollback(i, id, time.Now().UTC())
		if err != nil {
			return api.RollbackOperation{}, err
		}
		r, err := m.createCheckedRollbackLocked(ctx, acct, app, target, current, reason, id)
		if err != nil {
			return r, err
		}
		m.routeMonitorIncidents[app][j] = requested
		return r, nil
	}
	return api.RollbackOperation{}, ErrNotFound
}
