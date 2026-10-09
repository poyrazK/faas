package state

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

var _ RuntimeUpgradeReservationStore = (*MemStore)(nil)

func (m *MemStore) ReserveRuntimeUpgradeOperation(ctx context.Context, r RuntimeUpgradeOperationRequest, sourcePath string) (RuntimeUpgradeOperation, error) {
	if err := validateRuntimeUpgradeReservation(r, sourcePath); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	if old, ok := m.runtimeUpgradeOperations[r.ID]; ok {
		if old.RuntimeUpgradeOperationRequest != r || old.SourcePath != sourcePath {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		return old, nil
	}
	app, ok := m.apps[r.AppID]
	if !ok || app.AccountID != r.AccountID {
		return RuntimeUpgradeOperation{}, ErrNotFound
	}
	account := m.accounts[r.AccountID]
	serving, ok := m.deployments[r.ServingDeploymentID]
	limit, known := api.LimitsFor(account.Plan)
	if !known || !account.MayDeploy() || !ok || serving.AppID != app.ID || serving.Status != DeployLive || serving.DeletedAt != nil || serving.TrafficPercent != 100 ||
		app.Type != AppTypeFunction || app.Manifest.BuildDockerfile != "" || serving.SourceSHA256 != r.SourceSHA256 || serving.SourceBytes <= 0 || serving.SourceBytes > int64(limit.SourceTarballMaxMB)*1024*1024 ||
		(serving.Kind != DeploymentKindTarball && serving.Kind != DeploymentKindGitHub && serving.Kind != DeploymentKindPreview) || serving.CanaryTotalSteps != 0 || IsServiceRollout(serving) || serving.EnvironmentWorkloadHeld() {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if _, exists := m.deployments[r.DeploymentID]; exists {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	// Reuse the immutable customer artifact inputs, with fresh pipeline/output state.
	candidate := normalizeProjectCloneArtifact(projectCloneArtifactFromDeployment(serving)).deployment("", normalizedDeploymentScope(serving.Scope), ProjectEnvironmentWorkloadSettings{})
	candidate.ID, candidate.SourcePath, candidate.SourceBytes, candidate.SourceRoot = r.DeploymentID, sourcePath, serving.SourceBytes, serving.SourceRoot
	candidate.ImageDigest, candidate.RootfsPath, candidate.RootfsKey, candidate.RootfsBytes = "", "", "", 0
	candidate.InferredProfile, candidate.SecretReloadSignal, candidate.SecretReloadSignalKnown = nil, "", false
	candidate.MinInstances, candidate.Reason, candidate.CreatedAt = serving.MinInstances, "runtime-upgrade:"+r.ID, time.Now().UTC()
	candidate.Priority = 100
	candidate.RollbackOn5xx = serving.RollbackOn5xx
	m.putDeploymentLocked(candidate.ID, candidate)
	op, err := m.registerRuntimeUpgradeOperationLocked(r, RuntimeUpgradeReserved, sourcePath)
	if err != nil {
		delete(m.deployments, candidate.ID)
	}
	return op, err
}

func (m *MemStore) PrepareReservedRuntimeUpgradeOperation(ctx context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeOperation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	op, ok := m.runtimeUpgradeOperations[id]
	if !ok || op.AccountID != accountID {
		return RuntimeUpgradeOperation{}, ErrNotFound
	}
	if op.Phase != RuntimeUpgradeReserved {
		return op, nil
	}
	now := time.Now().UTC()
	phase, blocker, _, err := m.advanceRuntimeUpgradeOperationLocked(ctx, op, now)
	if err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	op.Phase, op.Blocker, op.NextAttemptAt = phase, blocker, now
	op.LeaseToken, op.LeaseUntil = "", time.Time{}
	if phase == RuntimeUpgradeBlocked {
		op.FinishedAt = now
	}
	m.runtimeUpgradeOperations[id] = op
	return op, nil
}

func (m *MemStore) CancelRuntimeUpgradeOperation(ctx context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeOperation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	op, ok := m.runtimeUpgradeOperations[id]
	if !ok || op.AccountID != accountID {
		return RuntimeUpgradeOperation{}, ErrNotFound
	}
	if op.Phase == RuntimeUpgradeCancelled {
		return op, nil
	}
	if !runtimeUpgradeActive(op.Phase) {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if cutover, ok := m.runtimeUpgradeCutovers[op.DeploymentID]; ok {
		if !op.cutover(cutover.WakeID).matches(cutover) {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		op.Phase, op.WakeID = RuntimeUpgradeComplete, cutover.WakeID
	} else {
		d, ok := m.deployments[op.DeploymentID]
		if !ok || d.TrafficPercent != 0 || !d.TrafficPercentExplicit {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		now := time.Now().UTC()
		if d.Status.IsCancelEligible() {
			d.Status, d.CancelledAt, d.CancelledByPrincipal, d.CancelReason = DeployCancelled, &now, accountID, string(CancelReasonUser)
			if err := finalizeCancelledDeploymentState(&d, now, CancelReasonUser); err != nil {
				return RuntimeUpgradeOperation{}, err
			}
			m.putDeploymentLocked(d.ID, d)
		}
		for taskID, task := range m.appTasks {
			if task.DeploymentID != d.ID || task.Kind != AppTaskKindRelease || task.Status.Terminal() {
				continue
			}
			if task.Status == AppTaskQueued {
				task.Status, task.FinishedAt = AppTaskCancelled, appTaskTimePtr(now)
			} else if task.CancelRequested == nil {
				task.CancelRequested = appTaskTimePtr(now)
			}
			task.UpdatedAt = now
			m.appTasks[taskID] = task
		}
		for buildID, b := range m.builds {
			if b.DeploymentID != d.ID || (b.Status != BuildQueued && b.Status != BuildRunning) {
				continue
			}
			if b.Status == BuildRunning {
				m.enqueueBuildVMCleanupLocked(buildID, now)
			}
			b.Status, b.CancelledAt, b.CancelledByDeploymentCascade = BuildCancelled, &now, true
			m.builds[buildID] = b
		}
		op.Phase = RuntimeUpgradeCancelled
	}
	op.LeaseToken, op.LeaseUntil, op.FinishedAt = "", time.Time{}, time.Now().UTC()
	m.runtimeUpgradeOperations[id] = op
	return op, nil
}
