package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

var _ OperatorRestartStore = (*MemStore)(nil)

func (m *MemStore) GetOperatorRestart(_ context.Context, id string) (OperatorRestartExecution, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.operatorRestarts[id]
	if !ok {
		return e, ErrNotFound
	}
	return cloneOperatorRestart(e), nil
}

func (m *MemStore) restartClaimLocked(id, token string) (OperatorIntent, error) {
	i, ok := m.operatorIntents[id]
	if !ok {
		return i, ErrOperatorIntentNotFound
	}
	if i.Kind != OperatorIntentKindForceRestart || i.Status != OperatorIntentRunning || i.ClaimToken != token {
		return i, ErrOperatorRestartClaimLost
	}
	return i, nil
}

func (m *MemStore) PrepareOperatorRestart(_ context.Context, id, token string, target OperatorRestartTarget) (OperatorRestartExecution, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorRestartExecution{}, err
	}
	if err := validateRestartTarget(target); err != nil {
		return OperatorRestartExecution{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, err := m.restartClaimLocked(id, token)
	if err != nil {
		return OperatorRestartExecution{}, err
	}
	if e, exists := m.operatorRestarts[id]; exists {
		return cloneOperatorRestart(e), nil
	}
	instance, exists := m.instances[target.InstanceID]
	target.MigrationLeaseToken, target.MigratedAt = instance.LeaseToken, instance.MigratedAt
	if !exists || !sameRestartID(i.TargetID, target.InstanceID) || !target.MatchesInstance(instance) || instance.State != string(StateRunning) {
		return OperatorRestartExecution{}, ErrOperatorRestartTargetChanged
	}
	target.SnapshotIDs = []string{}
	for _, tier := range []string{SnapshotTierWarm, SnapshotTierInit} {
		chosen := -1
		for index, snap := range m.snapshots {
			if snap.DeploymentID != target.DeploymentID || snap.Stale || snap.Tier != tier {
				continue
			}
			if chosen < 0 || snap.CreatedAt.After(m.snapshots[chosen].CreatedAt) ||
				(snap.CreatedAt.Equal(m.snapshots[chosen].CreatedAt) && snap.ID > m.snapshots[chosen].ID) {
				chosen = index
			}
		}
		if chosen >= 0 {
			m.snapshots[chosen].Stale = true
			target.SnapshotIDs = append(target.SnapshotIDs, m.snapshots[chosen].ID)
			m.deleteSnapshotReplicasLocked(m.snapshots[chosen].ID)
		}
	}
	e := OperatorRestartExecution{IntentID: id, Target: target, Phase: RestartPrepared, UpdatedAt: time.Now().UTC()}
	if m.operatorRestarts == nil {
		m.operatorRestarts = make(map[string]OperatorRestartExecution)
	}
	m.operatorRestarts[id] = cloneOperatorRestart(e)
	return cloneOperatorRestart(e), nil
}

func (m *MemStore) StartOperatorRestartDestroy(_ context.Context, id, token, nodeID string) (OperatorRestartExecution, bool, error) {
	if err := validRestartClaim(id, token); err != nil {
		return OperatorRestartExecution{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.restartClaimLocked(id, token); err != nil {
		return OperatorRestartExecution{}, false, err
	}
	e, ok := m.operatorRestarts[id]
	if !ok {
		return e, false, ErrNotFound
	}
	if !sameRestartID(e.Target.NodeID, nodeID) {
		return e, false, ErrOperatorRestartTargetChanged
	}
	if e.Phase != RestartPrepared {
		return cloneOperatorRestart(e), false, nil
	}
	instance, exists := m.instances[e.Target.InstanceID]
	if !exists || !e.Target.MatchesInstance(instance) || instance.State != string(StateRunning) {
		return e, false, ErrOperatorRestartTargetChanged
	}
	e.Phase, e.DestroyToken, e.UpdatedAt = RestartStarted, uuid.NewString(), time.Now().UTC()
	m.operatorRestarts[id] = e
	return cloneOperatorRestart(e), true, nil
}

func (m *MemStore) AcknowledgeOperatorRestartDestroy(_ context.Context, id, token string) error {
	if err := validRestartClaim(id, token); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.operatorRestarts[id]
	if !ok || e.DestroyToken != token {
		return ErrConflict
	}
	if e.Phase != RestartStarted && e.Phase != RestartOutcomeUnknown && e.Phase != RestartDestroyed && e.Phase != RestartCompleted {
		return ErrConflict
	}
	if e.DestroyedAt == nil {
		now := time.Now().UTC()
		e.DestroyedAt = &now
	}
	if e.Phase != RestartCompleted {
		e.Phase = RestartDestroyed
	}
	e.UpdatedAt = time.Now().UTC()
	m.operatorRestarts[id] = e
	return nil
}

func (m *MemStore) CompleteOperatorRestart(_ context.Context, input OperatorRestartCompletion) (OperatorRestartResult, error) {
	c, err := normalizeRestartCompletion(input)
	if err != nil {
		return OperatorRestartResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	i, ok := m.operatorIntents[c.IntentID]
	if !ok {
		return OperatorRestartResult{}, ErrOperatorIntentNotFound
	}
	if i.Kind != OperatorIntentKindForceRestart || i.ClaimToken != c.ClaimToken {
		return OperatorRestartResult{}, ErrOperatorRestartClaimLost
	}
	if i.Status != OperatorIntentRunning {
		if i.Status == c.Status && i.Error == c.Error && i.CompletionAuditOutboxID > 0 {
			return OperatorRestartResult{AuditOutboxID: i.CompletionAuditOutboxID, Replayed: true}, nil
		}
		return OperatorRestartResult{}, ErrConflict
	}
	e, hasExecution := m.operatorRestarts[i.ID]
	if c.Status == OperatorIntentFailed && e.DestroyToken != "" {
		return OperatorRestartResult{}, fmt.Errorf("started restart requires a proven outcome or outcome_unknown: %w", ErrConflict)
	}
	if c.Status == OperatorIntentSucceeded {
		if !hasExecution || e.DestroyedAt == nil {
			return OperatorRestartResult{}, fmt.Errorf("restart lacks a durable teardown acknowledgement: %w", ErrConflict)
		}
		instance, exists := m.instances[e.Target.InstanceID]
		if !exists || !e.Target.MatchesInstance(instance) || (instance.State != string(StateRunning) && instance.State != string(StateStopped)) {
			return OperatorRestartResult{}, ErrOperatorRestartTargetChanged
		}
		instance.State = string(StateStopped)
		if now := time.Now().UTC(); now.After(instance.LastRequestAt) {
			instance.LastRequestAt = now
		}
		if instance.TerminalAt == nil {
			now := time.Now().UTC()
			instance.TerminalAt = &now
		}
		m.instances[instance.ID] = instance
		if m.hostPorts != nil {
			m.hostPorts.Release(instance.NodeID, instance.ID)
		}
		e.Phase = RestartCompleted
	} else if hasExecution {
		if c.Status == OperatorIntentOutcomeUnknown {
			e.Phase = RestartOutcomeUnknown
			if e.DestroyedAt != nil {
				e.Phase = RestartDestroyed
			}
		} else {
			e.Phase = RestartFailed
		}
	}
	now := time.Now().UTC()
	i.Status, i.Error, i.FinishedAt = c.Status, c.Error, &now
	i.SnapIDsMarkedStale = []string{}
	var execution *OperatorRestartExecution
	if hasExecution {
		i.SnapIDsMarkedStale = append(i.SnapIDsMarkedStale, e.Target.SnapshotIDs...)
		e.UpdatedAt = now
		execution = &e
	}
	outboxID, err := m.enqueueAuditEventLocked("schedd", "operator.action.force_restart.outcome", i.AccountID, restartOutcomeAudit(i, execution), restartAuditKey(i))
	if err != nil {
		return OperatorRestartResult{}, err
	}
	i.CompletionAuditOutboxID = outboxID
	m.operatorIntents[i.ID] = i
	if hasExecution {
		m.operatorRestarts[i.ID] = e
	}
	return OperatorRestartResult{AuditOutboxID: outboxID}, nil
}

// Snapshot order is part of the operator result, independent of SQL row order.
func sortRestartSnapshots(ids []struct{ ID, Tier string }) []string {
	sort.Slice(ids, func(i, j int) bool { return ids[i].Tier == SnapshotTierWarm && ids[j].Tier != SnapshotTierWarm })
	result := make([]string, len(ids))
	for i := range ids {
		result[i] = ids[i].ID
	}
	return result
}
