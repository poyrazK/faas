package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

func (m *MemStore) CreateRuntimeConfigOperation(_ context.Context, config RuntimeConfig, actorID, reason string) (RuntimeConfigOperation, error) {
	if config.ApplyMode != RuntimeConfigApplyGraceful && config.ApplyMode != RuntimeConfigApplyRolling && config.ApplyMode != RuntimeConfigApplyBreakGlass {
		return RuntimeConfigOperation{}, fmt.Errorf("state: runtime config operation requires non-hot apply mode")
	}
	if !json.Valid(config.DesiredValue) {
		return RuntimeConfigOperation{}, fmt.Errorf("state: runtime config operation value is invalid json")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	op := RuntimeConfigOperation{
		ID: uuid.NewString(), Key: config.Key, Scope: config.Scope, ScopeID: config.ScopeID,
		Version: config.Version, DesiredValue: append(json.RawMessage(nil), config.DesiredValue...),
		ApplyMode: config.ApplyMode, Status: RuntimeConfigOperationPending, Phase: "queued",
		ActorID: actorID, Reason: reason, RequestedAt: now,
	}
	m.runtimeConfigOperations[op.ID] = op
	return cloneRuntimeConfigOperation(op), nil
}

func (m *MemStore) GetRuntimeConfigOperation(_ context.Context, id string) (RuntimeConfigOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.runtimeConfigOperations[id]
	if !ok {
		return RuntimeConfigOperation{}, ErrRuntimeConfigNotFound
	}
	return cloneRuntimeConfigOperation(op), nil
}

func (m *MemStore) ClaimPendingRuntimeConfigOperation(_ context.Context) (RuntimeConfigOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ops := make([]RuntimeConfigOperation, 0, len(m.runtimeConfigOperations))
	for _, op := range m.runtimeConfigOperations {
		if op.Status == RuntimeConfigOperationPending {
			ops = append(ops, op)
		}
	}
	if len(ops) == 0 {
		return RuntimeConfigOperation{}, ErrRuntimeConfigNotFound
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].RequestedAt.Before(ops[j].RequestedAt) })
	op := ops[0]
	op.Status = RuntimeConfigOperationRunning
	op.Phase = "claimed"
	now := time.Now().UTC()
	op.StartedAt = &now
	m.runtimeConfigOperations[op.ID] = op
	return cloneRuntimeConfigOperation(op), nil
}

func (m *MemStore) MarkRuntimeConfigOperationSucceeded(_ context.Context, id string, effectiveValue json.RawMessage, appliedCount, targetCount int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.runtimeConfigOperations[id]
	if !ok || op.Status != RuntimeConfigOperationRunning {
		return ErrRuntimeConfigNotFound
	}
	op.Status = RuntimeConfigOperationSucceeded
	op.Phase = "complete"
	op.EffectiveValue = append(json.RawMessage(nil), effectiveValue...)
	op.AppliedCount = appliedCount
	op.TargetCount = targetCount
	op.FailedCount = 0
	now := time.Now().UTC()
	op.FinishedAt = &now
	m.runtimeConfigOperations[id] = op
	configKey := runtimeConfigMapKey(op.Key, op.Scope, op.ScopeID)
	if row, ok := m.runtimeConfigs[configKey]; ok && row.Version == op.Version {
		row.EffectiveValue = append(json.RawMessage(nil), effectiveValue...)
		row.Status = RuntimeConfigApplied
		row.LastError = ""
		row.UpdatedAt = now
		row.AppliedAt = &now
		m.runtimeConfigs[configKey] = row
	}
	return nil
}

func (m *MemStore) MarkRuntimeConfigOperationFailed(_ context.Context, id, phase, errMsg string) error {
	return m.finishRuntimeConfigOperation(id, RuntimeConfigOperationFailed, phase, errMsg, false)
}

func (m *MemStore) MarkRuntimeConfigOperationBlocked(_ context.Context, id, phase, reason string) error {
	return m.finishRuntimeConfigOperation(id, RuntimeConfigOperationBlocked, phase, reason, true)
}

func (m *MemStore) finishRuntimeConfigOperation(id string, status RuntimeConfigOperationStatus, phase, message string, allowPending bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.runtimeConfigOperations[id]
	if !ok || (op.Status != RuntimeConfigOperationRunning && (!allowPending || op.Status != RuntimeConfigOperationPending)) {
		return ErrRuntimeConfigNotFound
	}
	if len(message) > 1024 {
		message = message[:1024]
	}
	op.Status = status
	op.Phase = phase
	op.Error = message
	now := time.Now().UTC()
	op.FinishedAt = &now
	m.runtimeConfigOperations[id] = op
	configKey := runtimeConfigMapKey(op.Key, op.Scope, op.ScopeID)
	if row, ok := m.runtimeConfigs[configKey]; ok && row.Version == op.Version {
		row.Status = RuntimeConfigStatus(status)
		row.LastError = message
		row.UpdatedAt = now
		m.runtimeConfigs[configKey] = row
	}
	return nil
}

func cloneRuntimeConfigOperation(op RuntimeConfigOperation) RuntimeConfigOperation {
	op.DesiredValue = append(json.RawMessage(nil), op.DesiredValue...)
	op.EffectiveValue = append(json.RawMessage(nil), op.EffectiveValue...)
	if op.StartedAt != nil {
		value := *op.StartedAt
		op.StartedAt = &value
	}
	if op.FinishedAt != nil {
		value := *op.FinishedAt
		op.FinishedAt = &value
	}
	return op
}
