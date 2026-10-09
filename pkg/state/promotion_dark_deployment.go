package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectPromotionDeploymentStore = (*PgStore)(nil)
var _ ProjectPromotionDeploymentStore = (*MemStore)(nil)

// MarkDeploymentLiveDark makes a prepared non-GitOps deployment eligible as a
// release graph member without changing the workload's weighted route. GitOps
// candidates require a graph-scoped activation transaction for their lifecycle.
func (s *PgStore) MarkDeploymentLiveDark(ctx context.Context, id string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: mark dark deployment live begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var appID string
	if err := tx.QueryRow(ctx, `select app_id from deployments where id = $1`, id).Scan(&appID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: mark dark deployment resolve app: %w", err)
	}
	if err := sqlc.New().LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return err
	}
	var found int
	if err := tx.QueryRow(ctx, `select 1 from apps where id = $1 for update`, appID).Scan(&found); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("state: mark dark deployment lock app: %w", err)
	}
	dep, err := scanDeploymentWithRootfs(tx.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+` from deployments where id = $1 for update`, id))
	if err != nil {
		return err
	}
	// Graph activation owns deployment promotion for every managed workload in
	// the scope, including after the temporary hold has been lifted.
	managed, err := environmentGitOpsManagedForScopeTx(ctx, tx, dep.AppID, dep.Scope)
	if err != nil {
		return fmt.Errorf("state: check managed workload before dark promotion: %w", err)
	}
	if managed {
		return ErrConflict
	}
	if err := s.checkDeploymentAutomations(ctx, tx, dep); err != nil {
		return err
	}
	if dep.Status == DeployLive && dep.TrafficPercent == 0 && dep.TrafficPercentExplicit {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("state: mark dark deployment idempotent commit: %w", err)
		}
		return nil
	}
	if dep.Status != DeployPending || dep.CanaryTotalSteps > 0 || IsServiceRollout(dep) ||
		dep.TrafficPercent != 0 || !dep.TrafficPercentExplicit {
		return ErrInvalidStateTransition
	}
	if _, err := tx.Exec(ctx, `update crons set suspended_reason = '' where app_id = $1 and suspended_reason <> ''`, appID); err != nil {
		return fmt.Errorf("state: reactivate dark deployment crons: %w", err)
	}
	if _, err := tx.Exec(ctx, `update deployments set status = 'live', error = '', rollout_state = 'complete',
		rollout_completed_at = coalesce(rollout_completed_at, now()) where id = $1`, id); err != nil {
		return fmt.Errorf("state: mark dark deployment live update: %w", err)
	}
	snap, policySnap, err := s.captureDeploymentOpenAPISnapshotTx(ctx, tx, dep)
	if err != nil {
		return err
	}
	if err := persistDeploymentSnapshotsDBTX(ctx, tx, snap, policySnap, dep.Status != DeployLive); err != nil {
		return fmt.Errorf("state: persist dark deployment snapshots: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: mark dark deployment live commit: %w", err)
	}
	return nil
}

func (m *MemStore) MarkDeploymentLiveDark(ctx context.Context, id string) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[id]
	if !ok {
		return ErrNotFound
	}
	if err := m.checkDeploymentAutomationsLocked(dep); err != nil {
		return err
	}
	before := dep
	previousStatus := dep.Status
	defer func() {
		if err == nil {
			if current, exists := m.deployments[id]; exists {
				m.enqueueRolloutOutcomeWebhooksLocked(before, current)
				if previousStatus == DeployLive || current.Status != DeployLive {
					return
				}
				m.enqueueDeploymentLifecycleWebhooksLocked(current)
			}
		}
	}()
	if m.environmentGitOpsManagedForScopeLocked(dep.AppID, dep.Scope) {
		return ErrConflict
	}
	if dep.Status == DeployLive && dep.TrafficPercent == 0 && dep.TrafficPercentExplicit {
		return nil
	}
	if dep.Status != DeployPending || dep.CanaryTotalSteps > 0 || IsServiceRollout(dep) ||
		dep.TrafficPercent != 0 || !dep.TrafficPercentExplicit {
		return ErrInvalidStateTransition
	}
	dep.Status = DeployLive
	dep.Error = ""
	dep.RolloutState = "complete"
	if dep.RolloutCompletedAt == nil {
		now := time.Now().UTC()
		dep.RolloutCompletedAt = &now
	}
	if err := m.captureAndStoreDeploymentSnapshotsLocked(ctx, dep, previousStatus != DeployLive); err != nil {
		return err
	}
	m.reactivateCronsForAppLocked(dep.AppID)
	m.deployments[id] = dep
	return nil
}
