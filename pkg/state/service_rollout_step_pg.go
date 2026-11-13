package state

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) ClaimServiceRolloutReplicaDrains(ctx context.Context, id string, percent int) (Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Deployment{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, live, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return target, err
	}
	if !serviceRolloutDrainClaimValid(target, percent) {
		return target, ErrServiceRolloutInvalid
	}
	if len(target.ServiceRolloutHandoff.RetiringInstanceIDs) > 0 {
		return target, nil
	}
	previous, found := previousServiceRolloutRow(target, live)
	if !found || previous.id != target.ServiceRolloutHandoff.PredecessorDeploymentID {
		return target, ErrServiceRolloutInvalid
	}
	q := sqlc.New()

	newReady, desired, err := pgServiceRolloutReadyReplicas(ctx, tx, target.AppID, target.ID)
	if err != nil {
		return target, err
	}
	if len(newReady) < ServiceRolloutReadyTarget(desired, percent) {
		return target, ErrServiceRolloutNotReady
	}
	oldReady, oldDesired, err := pgServiceRolloutReadyReplicas(ctx, tx, target.AppID, target.ServiceRolloutHandoff.PredecessorDeploymentID)
	if err != nil {
		return target, err
	}
	if len(oldReady) < ServiceRolloutPredecessorTarget(oldDesired, percent) {
		return target, ErrServiceRolloutNotReady
	}
	ids := selectServiceRolloutDrains(oldReady, oldDesired, percent)
	for _, id := range ids {
		changed, err := q.DrainServiceRolloutReplica(ctx, tx, mustPgUUID(id))
		if err != nil {
			return target, err
		}
		if changed != 1 {
			return target, ErrConflict
		}
	}
	target.ServiceRolloutHandoff.RetiringInstanceIDs = ids
	if err := saveServiceHandoff(ctx, tx, target); err != nil {
		return target, err
	}
	return target, tx.Commit(ctx)
}

func (s *PgStore) CompleteServiceRolloutReplicaDrains(ctx context.Context, id string, expected []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	target, _, err := s.loadAndLockServiceRollout(ctx, tx, id)
	if err != nil {
		return err
	}
	if !slices.Equal(expected, target.ServiceRolloutHandoff.RetiringInstanceIDs) {
		return ErrServiceRolloutInvalid
	}
	rows, err := sqlc.New().LockServiceRolloutRetiringStates(ctx, tx, expected)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if State(row.State) != StateStopped {
			return ErrServiceRolloutInvalid
		}
	}
	target.ServiceRolloutHandoff.RetiringInstanceIDs = nil
	if err := saveServiceHandoff(ctx, tx, target); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func pgServiceRolloutReadyReplicas(ctx context.Context, tx pgx.Tx, appID, deploymentID string) ([]string, int, error) {
	q := sqlc.New()
	facts, err := q.ServiceRolloutRecipientReadinessConfig(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return nil, 0, err
	}
	sources, err := DeploymentReadinessSources(Deployment{OverrideReadinessProbe: facts.OverrideReadinessProbe, Sidecars: facts.Sidecars})
	if err != nil {
		return nil, 0, err
	}
	rows, err := q.LockServiceRolloutReadyReplicas(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return nil, 0, err
	}
	var targets []ReadinessTarget
	for _, row := range rows {
		targets = append(targets, ReadinessTarget{AppID: appID, InstanceID: row.ID, NodeID: row.NodeID, WakeID: row.WakeID})
	}
	var ids []string
	for start := 0; start < len(targets); start += api.TrafficReadinessBatchSize {
		end := min(start+api.TrafficReadinessBatchSize, len(targets))
		signals, err := latestInstanceReadinessForTargets(ctx, tx, targets[start:end])
		if err != nil {
			return nil, 0, err
		}
		for _, ins := range targets[start:end] {
			if serviceReplicaReady(sources, signals[ins.InstanceID]) {
				ids = append(ids, ins.InstanceID)
			}
		}
	}
	return ids, int(facts.Desired), nil
}
