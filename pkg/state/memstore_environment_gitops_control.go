package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsControlStore = (*MemStore)(nil)

func (m *MemStore) UpdateEnvironmentGitSource(_ context.Context, accountID, sourceID string, update EnvironmentGitSourceUpdate) (EnvironmentGitSource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	source, err := changedEnvironmentGitSource(memory.source, update)
	if err != nil {
		return EnvironmentGitSource{}, err
	}
	source.Generation++
	memory.source = source
	touchGitOpsMemoryIntent(memory)
	return memory.source, nil
}

func (m *MemStore) SetEnvironmentGitOpsOverride(_ context.Context, accountID, sourceID string, request EnvironmentGitOpsOverrideRequest) error {
	if err := validateGitOpsOverride(request, time.Now()); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return ErrNotFound
	}
	field := environmentsync.Field{Resource: request.Resource, Path: request.Path}
	if _, ok := memory.owners[field.Key()]; !ok {
		return ErrNotFound
	}
	if memory.overrides == nil {
		memory.overrides = map[string]environmentsync.Override{}
	}
	memory.overrides[field.Key()] = environmentsync.Override{Resource: request.Resource, Path: request.Path, ExpiresAt: request.ExpiresAt}
	touchGitOpsMemoryIntent(memory)
	return nil
}

func (m *MemStore) RemoveEnvironmentGitOpsOverride(_ context.Context, accountID, sourceID, resource, path string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return ErrNotFound
	}
	key := (environmentsync.Field{Resource: resource, Path: path}).Key()
	if _, ok := memory.overrides[key]; !ok {
		return ErrNotFound
	}
	delete(memory.overrides, key)
	touchGitOpsMemoryIntent(memory)
	return nil
}
