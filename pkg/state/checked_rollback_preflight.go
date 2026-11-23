package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CheckedRollbackPreflightStore observes admission conditions without preparing
// the target. The locked checks in CreateCheckedRollback still govern admission.
type CheckedRollbackPreflightStore interface {
	CheckRollbackPreflight(ctx context.Context, accountID, appID, targetID, currentID string) error
}

func (m *MemStore) CheckRollbackPreflight(_ context.Context, account, app, target, current string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.apps[app]
	if !ok || a.AccountID != account || (a.Status != AppActive && a.Status != AppEvictedCold) {
		return ErrNotFound
	}
	d := m.deployments[target]
	if d.AppID != app || d.EnvironmentWorkloadHeld() || d.Status != DeploySuperseded && (d.Status != DeployLive || d.TrafficPercent != 0) {
		return ErrNoRollbackTarget
	}
	r := newCheckedRollback(app, d.Scope, target, current, "", false)
	if !rollbackPairValid(r, d, m.deployments[current], m.rollbackRowsLocked(r)) {
		return ErrCheckedRollbackChanged
	}
	if err := m.checkAppRollbackGraphLocked(app, d.Scope, target); err != nil {
		return err
	}
	if m.checkedRollbackReuseLiveLocked(d) && (d.EnvironmentWorkloadManaged() || m.environmentGitOpsManagedForScopeLocked(app, d.Scope)) {
		return ErrInvalidArgument
	}
	for _, op := range m.checkedRollbacks {
		if op.AppID == app && sameServingLane(op.Scope, d.Scope) && !rollbackTerminal(op) {
			return ErrConflict
		}
	}
	for _, key := range m.deploymentLayerKeysLocked(d) {
		if artifact, ok := m.layerArtifactRetention[key]; ok && artifact.State != LayerArtifactRetained {
			return ErrLayerArtifactRetired
		}
	}
	return nil
}

func (s *PgStore) CheckRollbackPreflight(ctx context.Context, account, app, target, current string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var scope string
	var managedTarget bool
	err = tx.QueryRow(ctx, `
		select d.scope, coalesce(d.environment_workload_runtime not in ('{}'::jsonb, 'null'::jsonb), false)
		  from deployments d join apps a on a.id = d.app_id
		 where d.id = $1 and a.id = $2 and a.account_id = $3 and d.deleted_at is null
		   and a.status in ('active', 'evicted_cold')
		   and (d.status = 'superseded' or d.status = 'live' and d.traffic_percent = 0)`, target, app, account).Scan(&scope, &managedTarget)
	if err != nil {
		return mapErr(err)
	}
	// The admission snapshot treats frozen environment inputs as held.
	if managedTarget {
		return ErrNoRollbackTarget
	}
	r := newCheckedRollback(app, scope, target, current, "", false)
	if err := rollbackCurrentMatches(ctx, tx, r); err != nil {
		return err
	}
	q := sqlc.New()
	blocked, err := q.ActiveGraphBlocksAppRollback(ctx, tx, sqlc.ActiveGraphBlocksAppRollbackParams{
		AppID: mustPgUUID(app), Scope: rollbackGraphScope(scope), TargetID: mustPgUUID(target),
	})
	if err != nil {
		return err
	}
	if blocked {
		return ErrProjectReleaseRollbackRequired
	}
	reusable, err := q.CheckedRollbackReuseLive(ctx, tx, mustPgUUID(target))
	if err != nil {
		return err
	}
	if reusable.Valid && reusable.Bool {
		managed, err := environmentGitOpsManagedForScopeTx(ctx, tx, app, scope)
		if err != nil {
			return err
		}
		if managed {
			return ErrInvalidArgument
		}
	}
	var pending bool
	err = tx.QueryRow(ctx, `select exists(
		select 1 from deployment_rollback_operations where app_id = $1
		  and scope = any($2::text[]) and status not in ('complete', 'failed'))`, app, servingLaneScopes(scope)).Scan(&pending)
	if err != nil {
		return err
	}
	if pending {
		return ErrConflict
	}
	keys, err := q.ReadDeploymentLayerArtifactKeys(ctx, tx, mustPgUUID(target))
	if err != nil || len(keys) == 0 {
		return err
	}
	var retired bool
	err = tx.QueryRow(ctx, `select exists(
		select 1 from layer_artifact_retention where state <> 'retained' and storage_key = any($1::text[]))`, keys).Scan(&retired)
	if err != nil {
		return err
	}
	if retired {
		return ErrLayerArtifactRetired
	}
	return nil
}
