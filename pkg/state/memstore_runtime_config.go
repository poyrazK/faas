package state

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
)

func runtimeConfigMapKey(key string, scope RuntimeConfigScope, scopeID string) string {
	return fmt.Sprintf("%s\x00%s\x00%s", key, scope, scopeID)
}

func (m *MemStore) ListRuntimeConfigs(_ context.Context, scope RuntimeConfigScope, scopeID string) ([]RuntimeConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]RuntimeConfig, 0, len(m.runtimeConfigs))
	for _, row := range m.runtimeConfigs {
		if scope != "" && row.Scope != scope {
			continue
		}
		if scopeID != "" && row.ScopeID != scopeID {
			continue
		}
		out = append(out, cloneRuntimeConfig(row))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Scope != out[j].Scope {
			return out[i].Scope < out[j].Scope
		}
		if out[i].ScopeID != out[j].ScopeID {
			return out[i].ScopeID < out[j].ScopeID
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func (m *MemStore) GetRuntimeConfig(_ context.Context, key string, scope RuntimeConfigScope, scopeID string) (RuntimeConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.runtimeConfigs[runtimeConfigMapKey(key, scope, scopeID)]
	if !ok {
		return RuntimeConfig{}, ErrRuntimeConfigNotFound
	}
	return cloneRuntimeConfig(row), nil
}

func (m *MemStore) UpsertRuntimeConfig(_ context.Context, update RuntimeConfigUpdate) (RuntimeConfig, error) {
	if update.Scope == "" {
		update.Scope = RuntimeConfigScopeGlobal
	}
	if update.ApplyMode == "" {
		update.ApplyMode = RuntimeConfigApplyHot
	}
	rolloutPercent := 100
	if update.RolloutPercent != nil {
		rolloutPercent = *update.RolloutPercent
	}
	if rolloutPercent < 0 || rolloutPercent > 100 {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config rollout percent must be between 0 and 100")
	}
	if update.DesiredValue == nil || !json.Valid(update.DesiredValue) {
		return RuntimeConfig{}, fmt.Errorf("state: runtime config value is invalid json")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mapKey := runtimeConfigMapKey(update.Key, update.Scope, update.ScopeID)
	row, exists := m.runtimeConfigs[mapKey]
	var oldValue json.RawMessage
	if !exists {
		if update.ExpectedVersion != nil && *update.ExpectedVersion != 0 {
			return RuntimeConfig{}, ErrRuntimeConfigConflict
		}
		row = RuntimeConfig{
			ID:      uuid.NewString(),
			Key:     update.Key,
			Scope:   update.Scope,
			ScopeID: update.ScopeID,
			Version: 1,
		}
	} else {
		if update.ExpectedVersion != nil && *update.ExpectedVersion != row.Version {
			return RuntimeConfig{}, ErrRuntimeConfigConflict
		}
		oldValue = append(json.RawMessage(nil), row.DesiredValue...)
		row.Version++
	}
	row.DesiredValue = append(json.RawMessage(nil), update.DesiredValue...)
	row.EffectiveValue = nil
	row.RolloutPercent = rolloutPercent
	row.ApplyMode = update.ApplyMode
	row.Status = RuntimeConfigPending
	row.LastError = ""
	row.ActorID = update.ActorID
	row.Reason = update.Reason
	row.UpdatedAt = time.Now().UTC()
	row.AppliedAt = nil
	m.runtimeConfigs[mapKey] = row
	m.runtimeConfigRevisions = append(m.runtimeConfigRevisions, RuntimeConfigRevision{
		ID: int64(len(m.runtimeConfigRevisions) + 1), Key: row.Key, Scope: row.Scope,
		ScopeID: row.ScopeID, Version: row.Version,
		RolloutPercent: row.RolloutPercent,
		OldValue:       append(json.RawMessage(nil), oldValue...),
		NewValue:       append(json.RawMessage(nil), row.DesiredValue...),
		ActorID:        row.ActorID, Reason: row.Reason, CreatedAt: row.UpdatedAt,
	})
	return cloneRuntimeConfig(row), nil
}

func (m *MemStore) ListRuntimeConfigRevisions(_ context.Context, key string, scope RuntimeConfigScope, scopeID string, limit int) ([]RuntimeConfigRevision, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RuntimeConfigRevision
	for i := len(m.runtimeConfigRevisions) - 1; i >= 0 && len(out) < limit; i-- {
		revision := m.runtimeConfigRevisions[i]
		if revision.Key != key || revision.Scope != scope || revision.ScopeID != scopeID {
			continue
		}
		revision.OldValue = append(json.RawMessage(nil), revision.OldValue...)
		revision.NewValue = append(json.RawMessage(nil), revision.NewValue...)
		out = append(out, revision)
	}
	return out, nil
}

func (m *MemStore) GetRuntimeConfigRevision(_ context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64) (RuntimeConfigRevision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.runtimeConfigRevisions) - 1; i >= 0; i-- {
		revision := m.runtimeConfigRevisions[i]
		if revision.Key != key || revision.Scope != scope || revision.ScopeID != scopeID || revision.Version != version {
			continue
		}
		revision.OldValue = append(json.RawMessage(nil), revision.OldValue...)
		revision.NewValue = append(json.RawMessage(nil), revision.NewValue...)
		return revision, nil
	}
	return RuntimeConfigRevision{}, ErrRuntimeConfigNotFound
}

func (m *MemStore) MarkRuntimeConfigApplied(_ context.Context, key string, scope RuntimeConfigScope, scopeID string, version int64, effectiveValue json.RawMessage, applyErr string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mapKey := runtimeConfigMapKey(key, scope, scopeID)
	row, ok := m.runtimeConfigs[mapKey]
	if !ok || row.Version != version {
		return ErrRuntimeConfigConflict
	}
	row.EffectiveValue = append(json.RawMessage(nil), effectiveValue...)
	row.LastError = applyErr
	row.UpdatedAt = time.Now().UTC()
	if applyErr == "" {
		row.Status = RuntimeConfigApplied
		now := row.UpdatedAt
		row.AppliedAt = &now
	} else {
		row.Status = RuntimeConfigFailed
		row.AppliedAt = nil
	}
	m.runtimeConfigs[mapKey] = row
	return nil
}

func cloneRuntimeConfig(row RuntimeConfig) RuntimeConfig {
	row.DesiredValue = append(json.RawMessage(nil), row.DesiredValue...)
	row.EffectiveValue = append(json.RawMessage(nil), row.EffectiveValue...)
	if row.AppliedAt != nil {
		value := *row.AppliedAt
		row.AppliedAt = &value
	}
	return row
}
