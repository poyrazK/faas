package state

import (
	"context"
	"time"
)

var (
	_ SnapshotRecoveryStore = (*MemStore)(nil)
	_ OperatorColdBootStore = (*MemStore)(nil)
)

func (m *MemStore) BindInstanceSnapshotRecovery(ctx context.Context, instanceID, wakeID, snapshotID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSnapshotRecoveryIDs(instanceID, wakeID); err != nil {
		return err
	}
	if snapshotID != "" {
		if err := validateSnapshotRecoveryIDs(snapshotID); err != nil {
			return err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.instances[instanceID]
	expected := string(StateColdBooting)
	if snapshotID != "" {
		expected = string(StateWaking)
	}
	if !ok || i.WakeID != wakeID || i.State != expected {
		return ErrConflict
	}
	if existing, ok := m.instanceSnapshotRecovery[instanceID]; ok {
		if existing.WakeID == wakeID && existing.SnapshotID == snapshotID {
			return nil
		}
		return ErrConflict
	}
	generation := m.snapshotRecoveryGenerations[i.DeploymentID]
	if snapshotID != "" {
		valid := false
		for _, snap := range m.snapshots {
			if snap.ID == snapshotID && snap.DeploymentID == i.DeploymentID && !snap.Stale && !snap.DeletePending && snap.RecoveryGeneration == generation {
				valid = true
				break
			}
		}
		if !valid {
			return ErrConflict
		}
	}
	if m.instanceSnapshotRecovery == nil {
		m.instanceSnapshotRecovery = make(map[string]instanceSnapshotRecovery)
	}
	m.instanceSnapshotRecovery[instanceID] = instanceSnapshotRecovery{WakeID: wakeID, SnapshotID: snapshotID, Generation: generation}
	return nil
}

func (m *MemStore) instanceSnapshotRecoveryFreshLocked(instanceID string) bool {
	i, exists := m.instances[instanceID]
	generation := m.snapshotRecoveryGenerations[i.DeploymentID]
	r, bound := m.instanceSnapshotRecovery[instanceID]
	return exists && (generation == 0 || bound && r.WakeID == i.WakeID && r.Generation == generation)
}

func (m *MemStore) InstanceSnapshotRecoveryFresh(ctx context.Context, instanceID string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.instances[instanceID]; !exists {
		return false, ErrNotFound
	}
	return m.instanceSnapshotRecoveryFreshLocked(instanceID), nil
}

func (m *MemStore) snapshotRecoveryPlanLocked(deploymentID string) SnapshotRecoveryResult {
	ids := []struct{ ID, Tier string }{}
	for _, snap := range m.snapshots {
		if snap.DeploymentID == deploymentID && !snap.Stale {
			ids = append(ids, struct{ ID, Tier string }{snap.ID, snap.Tier})
		}
	}
	return SnapshotRecoveryResult{Generation: m.snapshotRecoveryGenerations[deploymentID] + 1, SnapshotIDs: sortRestartSnapshots(ids)}
}

func (m *MemStore) applySnapshotRecoveryLocked(deploymentID string, result SnapshotRecoveryResult) {
	if m.snapshotRecoveryGenerations == nil {
		m.snapshotRecoveryGenerations = make(map[string]int64)
	}
	m.snapshotRecoveryGenerations[deploymentID] = result.Generation
	d := m.deployments[deploymentID]
	d.SnapshotMissCount, d.SnapshotMissBackoffUntil = 0, nil
	m.deployments[deploymentID] = d
	for j := range m.snapshots {
		if snap := &m.snapshots[j]; snap.DeploymentID == deploymentID && !snap.Stale {
			m.markSnapshotStaleLocked(j)
			m.deleteSnapshotReplicasLocked(snap.ID)
		}
	}
}

func (m *MemStore) recoveryDeploymentAvailableLocked(deploymentID string, accountID *string) bool {
	d, ok := m.deployments[deploymentID]
	app, found := m.apps[d.AppID]
	return ok && found && app.Status != AppDeleted && (accountID == nil || *accountID == app.AccountID)
}

func (m *MemStore) InvalidateSnapshotRecovery(ctx context.Context, deploymentID string) (SnapshotRecoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return SnapshotRecoveryResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.recoveryDeploymentAvailableLocked(deploymentID, nil) {
		return SnapshotRecoveryResult{}, ErrNotFound
	}
	result := m.snapshotRecoveryPlanLocked(deploymentID)
	m.applySnapshotRecoveryLocked(deploymentID, result)
	return result, nil
}

func (m *MemStore) ExecuteOperatorColdBoot(ctx context.Context, id, token string) (OperatorColdBootResult, error) {
	if err := ctx.Err(); err != nil {
		return OperatorColdBootResult{}, err
	}
	if err := validateSnapshotRecoveryIDs(id, token); err != nil {
		return OperatorColdBootResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, exists := m.operatorIntents[id]
	if !exists {
		return OperatorColdBootResult{}, ErrOperatorIntentNotFound
	}
	if i.Kind != OperatorIntentKindForceColdBoot || i.ClaimToken != token {
		return OperatorColdBootResult{}, ErrOperatorColdBootClaimLost
	}
	if i.Status != OperatorIntentRunning {
		if (i.Status == OperatorIntentSucceeded || i.Status == OperatorIntentFailed) && i.CompletionAuditOutboxID > 0 {
			return coldBootResult(i, true), nil
		}
		return OperatorColdBootResult{}, ErrConflict
	}
	i.Status, i.Error, i.SnapIDsMarkedStale = OperatorIntentFailed, "Cold-boot target deployment is unavailable.", []string{}
	var recovery SnapshotRecoveryResult
	if m.recoveryDeploymentAvailableLocked(i.TargetID, i.AccountID) {
		recovery = m.snapshotRecoveryPlanLocked(i.TargetID)
		i.Status, i.Error = OperatorIntentSucceeded, ""
		i.ColdBootGeneration, i.SnapIDsMarkedStale = recovery.Generation, recovery.SnapshotIDs
	}
	now := time.Now().UTC()
	i.FinishedAt = &now
	outboxID, err := m.enqueueAuditEventLocked("schedd", "operator.action.force_cold_boot.outcome", i.AccountID,
		coldBootOutcomeAudit(i), "operator.cold_boot.outcome:"+i.ID)
	if err != nil {
		return OperatorColdBootResult{}, err
	}
	if i.Status == OperatorIntentSucceeded {
		m.applySnapshotRecoveryLocked(i.TargetID, recovery)
	}
	i.CompletionAuditOutboxID = outboxID
	m.operatorIntents[id] = i
	return coldBootResult(i, false), nil
}
