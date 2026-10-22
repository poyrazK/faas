package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

func (m *MemStore) PreviewEnvironmentGitOpsCommit(_ context.Context, expected EnvironmentGitSource, commit string, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitPlanResponse, error) {
	if err := validateEnvironmentGitOpsPreview(expected, commit, desired); err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[expected.ID]
	if !ok || memory.source.AccountID != expected.AccountID || memory.source.Detached {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrNotFound
	}
	if memory.source.Generation != expected.Generation || memory.source.Spec != expected.Spec {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrConflict
	}
	if desired.Definition.Project != m.projects[memory.source.ProjectID].Slug || desired.Definition.Environment != memory.source.EnvironmentSlug {
		return api.PreviewEnvironmentGitPlanResponse{}, ErrInvalidArgument
	}
	if _, err := m.projectEnvironmentBySlugLocked(memory.source.ProjectID, memory.source.EnvironmentSlug); err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	observation, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return api.PreviewEnvironmentGitPlanResponse{}, err
	}
	return environmentGitOpsCommitPreview(memory.source, commit, desired, observation)
}
