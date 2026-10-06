package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ RuntimeUpgradeOperationStore = (*MemStore)(nil)

func (m *MemStore) RegisterRuntimeUpgradeOperation(ctx context.Context, r RuntimeUpgradeOperationRequest) (RuntimeUpgradeOperation, error) {
	if err := r.validate(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	return m.registerRuntimeUpgradeOperationLocked(r, RuntimeUpgradePrepared, "")
}

func (m *MemStore) registerRuntimeUpgradeOperationLocked(r RuntimeUpgradeOperationRequest, phase RuntimeUpgradeOperationPhase, sourcePath string) (RuntimeUpgradeOperation, error) {
	d, exists := m.deployments[r.DeploymentID]
	app := m.apps[d.AppID]
	if !exists || d.AppID != r.AppID || app.AccountID != r.AccountID || app.Status != AppActive || app.DeletedAt != nil || d.DeletedAt != nil {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	if old, ok := m.runtimeUpgradeOperations[r.ID]; ok {
		if old.RuntimeUpgradeOperationRequest != r || old.Phase == RuntimeUpgradeReserved {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		return old, nil
	}
	for _, op := range m.runtimeUpgradeOperations {
		if op.DeploymentID == d.ID || (op.AppID == app.ID && runtimeUpgradeActive(op.Phase)) {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
	}
	if app.Manifest.ExecutionMode == api.ExecutionModeJob || app.Manifest.ExecutionMode == api.ExecutionModeService || runtimeUpgradeOperationCandidate(d, r) != nil || d.Status != DeployPending || d.RootfsKey != "" || d.RootfsPath != "" || d.ImageDigest != "" {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	for _, b := range m.builds {
		if b.DeploymentID == d.ID || b.ID == r.ID {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
	}
	target, ok := m.runtimeReleases[r.TargetReleaseID]
	qualification := m.runtimeReleaseQualifications[r.TargetReleaseID]
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !ok || validateRuntimeUpgradeTarget(app, d, target) != nil || !validRuntimeReleaseQualification(target, qualification, now) || qualification.ReportSHA256 != r.QualificationReportSHA256 {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	pin := runtimeUpgradeTargetInput(d, r.TargetReleaseID)
	if old, ok := m.runtimeUpgradeTargets[d.ID]; ok && old != pin {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	baseline, err := m.runtimeUpgradeBaselineForTargetLocked(d, r.ServingDeploymentID, pin)
	if err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	if old, ok := m.runtimeUpgradeBaselines[d.ID]; ok {
		if !sameRuntimeUpgradeBaseline(old, baseline) {
			return RuntimeUpgradeOperation{}, ErrConflict
		}
		baseline = old
	} else {
		baseline.CapturedAt = now
	}
	if m.runtimeUpgradeTargets == nil {
		m.runtimeUpgradeTargets = make(map[string]runtimeUpgradeTarget)
	}
	if m.runtimeUpgradeBaselines == nil {
		m.runtimeUpgradeBaselines = make(map[string]RuntimeUpgradeBaseline)
	}
	if m.runtimeUpgradeOperations == nil {
		m.runtimeUpgradeOperations = make(map[string]RuntimeUpgradeOperation)
	}
	op := RuntimeUpgradeOperation{RuntimeUpgradeOperationRequest: r, Phase: phase, SourcePath: sourcePath, CreatedAt: now, NextAttemptAt: now, DeadlineAt: now.Add(api.RuntimeUpgradeOperationMaxAge)}
	m.runtimeUpgradeTargets[d.ID], m.runtimeUpgradeBaselines[d.ID], m.runtimeUpgradeOperations[r.ID] = pin, baseline, op
	return op, nil
}

func (m *MemStore) RuntimeUpgradeOperation(_ context.Context, accountID, id string) (RuntimeUpgradeOperation, error) {
	if validateRuntimeAppEnvIDs(accountID, id, id) != nil {
		return RuntimeUpgradeOperation{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.runtimeUpgradeOperations[id]
	if !ok || op.AccountID != accountID {
		return RuntimeUpgradeOperation{}, ErrNotFound
	}
	return op, nil
}

func (m *MemStore) ClaimRuntimeUpgradeOperation(ctx context.Context) (RuntimeUpgradeOperationClaim, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperationClaim{}, err
	}
	now := time.Now().UTC()
	var ops []RuntimeUpgradeOperation
	for _, op := range m.runtimeUpgradeOperations {
		if (op.Phase == RuntimeUpgradePrepared || op.Phase == RuntimeUpgradeWaiting || (op.Phase == RuntimeUpgradeReserved && !op.DeadlineAt.After(now))) && !op.NextAttemptAt.After(now) && !op.LeaseUntil.After(now) {
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		return RuntimeUpgradeOperationClaim{}, ErrNotFound
	}
	sort.Slice(ops, func(i, j int) bool {
		if !ops[i].NextAttemptAt.Equal(ops[j].NextAttemptAt) {
			return ops[i].NextAttemptAt.Before(ops[j].NextAttemptAt)
		}
		if ops[i].CreatedAt.Equal(ops[j].CreatedAt) {
			return ops[i].ID < ops[j].ID
		}
		return ops[i].CreatedAt.Before(ops[j].CreatedAt)
	})
	op := ops[0]
	op.LeaseToken, op.LeaseUntil = uuid.NewString(), now.Add(api.RuntimeUpgradeOperationLease)
	m.runtimeUpgradeOperations[op.ID] = op
	return RuntimeUpgradeOperationClaim{ID: op.ID, LeaseToken: op.LeaseToken}, nil
}

func (m *MemStore) AdvanceRuntimeUpgradeOperation(ctx context.Context, c RuntimeUpgradeOperationClaim) (RuntimeUpgradeOperation, error) {
	if err := c.validate(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	op, ok := m.runtimeUpgradeOperations[c.ID]
	now := time.Now().UTC()
	if !ok || op.LeaseToken != c.LeaseToken || !op.LeaseUntil.After(now) || !runtimeUpgradeActive(op.Phase) {
		return RuntimeUpgradeOperation{}, ErrConflict
	}
	phase, blocker, wake, err := m.advanceRuntimeUpgradeOperationLocked(ctx, op, now)
	if err != nil {
		return RuntimeUpgradeOperation{}, err
	}
	op.Phase, op.Blocker, op.WakeID = phase, blocker, wake
	op.LeaseToken, op.LeaseUntil = "", time.Time{}
	op.NextAttemptAt = now.Add(api.RuntimeUpgradeOperationInterval)
	if phase == RuntimeUpgradeComplete || phase == RuntimeUpgradeBlocked {
		op.FinishedAt = now
	}
	m.runtimeUpgradeOperations[op.ID] = op
	return op, nil
}

func (m *MemStore) advanceRuntimeUpgradeOperationLocked(ctx context.Context, op RuntimeUpgradeOperation, now time.Time) (RuntimeUpgradeOperationPhase, string, string, error) {
	r := op.RuntimeUpgradeOperationRequest
	if old, ok := m.runtimeUpgradeCutovers[r.DeploymentID]; ok {
		if !r.cutover(old.WakeID).matches(old) {
			return blockedRuntimeUpgradeOperation("intent_changed")
		}
		return RuntimeUpgradeComplete, "", old.WakeID, nil
	}
	if !op.DeadlineAt.After(now) {
		return blockedRuntimeUpgradeOperation("deadline_exceeded")
	}
	d, ok := m.deployments[r.DeploymentID]
	app := m.apps[d.AppID]
	if !ok || app.ID != r.AppID || app.AccountID != r.AccountID || app.Status != AppActive || app.DeletedAt != nil || app.Manifest.ExecutionMode == api.ExecutionModeJob || app.Manifest.ExecutionMode == api.ExecutionModeService || runtimeUpgradeOperationCandidate(d, r) != nil || (op.SourcePath != "" && d.SourcePath != op.SourcePath) || runtimeUpgradeOperationTerminal(d) {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	baseline := m.runtimeUpgradeBaselines[d.ID]
	pin := m.runtimeUpgradeTargets[d.ID]
	current, err := m.runtimeUpgradeBaselineLocked(d, r.ServingDeploymentID)
	if err != nil || baseline.ServingDeploymentID != r.ServingDeploymentID || pin.ReleaseID != r.TargetReleaseID || !sameRuntimeUpgradeBaseline(baseline, current) {
		return blockedRuntimeUpgradeOperation("baseline_changed")
	}
	target := m.runtimeReleases[r.TargetReleaseID]
	qualification := m.runtimeReleaseQualifications[r.TargetReleaseID]
	if qualification.ReportSHA256 != r.QualificationReportSHA256 || !validRuntimeReleaseQualification(target, qualification, now) {
		return blockedRuntimeUpgradeOperation("qualification_changed")
	}
	if op.Phase != RuntimeUpgradeReserved && !op.LeaseUntil.After(time.Now().UTC()) {
		return "", "", "", ErrConflict
	}
	if !op.DeadlineAt.After(time.Now().UTC()) {
		return blockedRuntimeUpgradeOperation("deadline_exceeded")
	}
	if op.Phase == RuntimeUpgradePrepared || op.Phase == RuntimeUpgradeReserved {
		if d.Status != DeployPending || d.RootfsKey != "" || d.RootfsPath != "" || d.ImageDigest != "" {
			return blockedRuntimeUpgradeOperation("candidate_changed")
		}
		for _, b := range m.builds {
			if b.DeploymentID == d.ID || b.ID == r.ID {
				return blockedRuntimeUpgradeOperation("candidate_changed")
			}
		}
		if op.Phase == RuntimeUpgradeReserved {
			return RuntimeUpgradePrepared, "", "", nil
		}
		m.builds[r.ID] = Build{ID: r.ID, DeploymentID: d.ID, Kind: d.Kind, SourceBytes: d.SourceBytes, Status: BuildQueued, LogPath: d.LogPath, EnqueuedAt: now}
		d.Status, d.BuildID = DeployBuilding, r.ID
		m.putDeploymentLocked(d.ID, d)
		return RuntimeUpgradeWaiting, "", "", nil
	}
	if d.BuildID != r.ID {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	build, exists := m.builds[r.ID]
	if !exists || build.DeploymentID != d.ID || (build.Status != BuildQueued && build.Status != BuildRunning && build.Status != BuildSucceeded) || (d.Status == DeployLive && build.Status != BuildSucceeded) {
		return blockedRuntimeUpgradeOperation("candidate_changed")
	}
	if d.Status != DeployLive {
		return RuntimeUpgradeWaiting, "", "", nil
	}
	a, ready := m.runtimeUpgradeAcceptances[d.ID]
	if !ready {
		return RuntimeUpgradeWaiting, "", "", nil
	}
	if err := m.validateRuntimeUpgradeAcceptanceLocked(d.ID); err != nil {
		return blockedRuntimeUpgradeOperation("readiness_changed")
	}
	cutover, err := m.cutoverRuntimeUpgradeLocked(ctx, r.cutover(a.WakeID))
	if err != nil {
		return "", "", "", err
	}
	return RuntimeUpgradeComplete, "", cutover.WakeID, nil
}
