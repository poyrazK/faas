package state

import (
	"context"
	"slices"
	"time"
)

var _ EnvironmentGitOpsEffectStore = (*MemStore)(nil)

func (m *MemStore) PendingEnvironmentGitOpsEffects(_ context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsEffect, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	out := []EnvironmentGitOpsEffect{}
	for _, effect := range memory.effects {
		if effect.CompletedAt == nil {
			out = append(out, cloneEnvironmentGitOpsEffect(effect))
		}
	}
	slices.SortFunc(out, func(a, b EnvironmentGitOpsEffect) int {
		if a.GatewayGeneration < b.GatewayGeneration {
			return -1
		}
		if a.GatewayGeneration > b.GatewayGeneration {
			return 1
		}
		return 0
	})
	return out, nil
}

func (m *MemStore) pendingGitOpsEffectLocked(lease EnvironmentGitOpsLease, id string) (*environmentGitOpsMemory, EnvironmentGitOpsEffect, error) {
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, EnvironmentGitOpsEffect{}, err
	}
	effect, exists := memory.effects[id]
	if !exists || effect.CompletedAt != nil {
		return nil, effect, ErrConflict
	}
	return memory, effect, nil
}

func (m *MemStore) ExtendEnvironmentGitOpsEffectTargets(_ context.Context, lease EnvironmentGitOpsLease, id string, nodes []string) (EnvironmentGitOpsEffect, error) {
	nodes, err := canonicalGitOpsEffectNames(nodes)
	if err != nil {
		return EnvironmentGitOpsEffect{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, effect, err := m.pendingGitOpsEffectLocked(lease, id)
	if err != nil {
		return effect, err
	}
	effect.ExpectedNodes, _ = canonicalGitOpsEffectNames(append(slices.Clone(effect.ExpectedNodes), nodes...))
	memory.effects[id] = effect
	return cloneEnvironmentGitOpsEffect(effect), nil
}

func (m *MemStore) AcknowledgeEnvironmentGitOpsEffect(_ context.Context, lease EnvironmentGitOpsLease, id string, generation int64, node string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, effect, err := m.pendingGitOpsEffectLocked(lease, id)
	if err != nil {
		return err
	}
	if effect.GatewayGeneration != generation || !slices.Contains(effect.ExpectedNodes, node) {
		return ErrConflict
	}
	effect.AcknowledgedNodes, _ = canonicalGitOpsEffectNames(append(slices.Clone(effect.AcknowledgedNodes), node))
	memory.effects[id] = effect
	return nil
}

func (m *MemStore) CompleteEnvironmentGitOpsEffect(_ context.Context, lease EnvironmentGitOpsLease, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, effect, err := m.pendingGitOpsEffectLocked(lease, id)
	if err != nil {
		return err
	}
	for _, node := range effect.ExpectedNodes {
		if !slices.Contains(effect.AcknowledgedNodes, node) {
			return ErrConflict
		}
	}
	now := time.Now().UTC()
	effect.CompletedAt = &now
	memory.effects[id] = effect
	return nil
}
