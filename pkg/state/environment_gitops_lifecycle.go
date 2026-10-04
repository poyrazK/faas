package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) detachGitSourceLocked(accountID, sourceID string, generation int64) error {
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return ErrNotFound
	}
	if memory.source.Detached || memory.source.Generation != generation || memory.source.Spec.Mode != "report" {
		return ErrConflict
	}
	for _, effect := range memory.effects {
		if effect.CompletedAt == nil {
			return ErrConflict
		}
	}
	for _, effect := range memory.runtime {
		if effect.CompletedAt == nil {
			return ErrConflict
		}
	}
	for _, graph := range memory.graphs {
		if graph.Phase == "preparing" {
			return ErrConflict
		}
	}
	if len(memory.qualifications) > 0 {
		return ErrConflict
	}
	memory.source.Detached = true
	memory.source.Suspended = true
	memory.source.Generation++
	memory.source.UpdatedAt = time.Now().UTC()
	now := time.Now().UTC()
	for id, run := range memory.runs {
		if run.CompletedAt == nil {
			run.Status = "superseded"
			run.CompletedAt = &now
			memory.runs[id] = run
		}
	}
	memory.owners = nil
	memory.overrides = nil
	touchGitOpsMemoryIntent(memory)
	return nil
}
func (m *MemStore) DetachEnvironmentGitSource(_ context.Context, accountID, sourceID string, generation int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.detachGitSourceLocked(accountID, sourceID, generation)
}
func (m *MemStore) RebindEnvironmentGitSource(_ context.Context, accountID, sourceID string, generation int64, spec EnvironmentGitSourceSpec) (EnvironmentGitSource, error) {
	if spec.Validate() != nil || spec.Mode != "report" || spec.Prune {
		return EnvironmentGitSource{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	project := m.projects[memory.source.ProjectID]
	if project.RepoFullName != spec.Repository || project.InstallID != spec.InstallationID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	if env, err := m.projectEnvironmentBySlugLocked(memory.source.ProjectID, memory.source.EnvironmentSlug); err != nil || env.ID != memory.source.EnvironmentID {
		return EnvironmentGitSource{}, ErrNotFound
	}
	if err := m.detachGitSourceLocked(accountID, sourceID, generation); err != nil {
		return EnvironmentGitSource{}, err
	}
	return m.createEnvironmentGitSourceLocked(accountID, memory.source.ProjectID, memory.source.EnvironmentSlug, spec)
}

func detachGitSourceSQL(ctx context.Context, tx pgx.Tx, source EnvironmentGitSource, generation int64) error {
	if source.Detached || source.Generation != generation || source.Spec.Mode != "report" {
		return ErrConflict
	}
	q := sqlc.New()
	pending, err := q.EnvironmentGitOpsLifecyclePending(ctx, tx, mustPgUUID(source.ID))
	if err != nil {
		return err
	}
	if !pending.Valid || pending.Bool {
		return ErrConflict
	}
	if err = q.ReleaseEnvironmentGitSourceOverrides(ctx, tx, mustPgUUID(source.ID)); err != nil {
		return err
	}
	if err = q.ReleaseEnvironmentGitSourceOwners(ctx, tx, mustPgUUID(source.ID)); err != nil {
		return err
	}
	if err = q.SupersedeEnvironmentGitOpsRuns(ctx, tx, sqlc.SupersedeEnvironmentGitOpsRunsParams{SourceID: mustPgUUID(source.ID), NowAt: gitOpsTime(time.Now())}); err != nil {
		return err
	}
	n, err := q.DetachEnvironmentGitSource(ctx, tx, sqlc.DetachEnvironmentGitSourceParams{SourceID: mustPgUUID(source.ID), ExpectedGeneration: generation})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrConflict
	}
	return recordGitOpsEvent(ctx, tx, source.ID, source.AccountID, "detached", map[string]any{"generation": generation, "preserved_values": true})
}
func (s *PgStore) DetachEnvironmentGitSource(ctx context.Context, accountID, sourceID string, generation int64) error {
	return s.withGitOpsControl(ctx, accountID, sourceID, func(tx pgx.Tx, source EnvironmentGitSource) error {
		return detachGitSourceSQL(ctx, tx, source, generation)
	})
}
func (s *PgStore) RebindEnvironmentGitSource(ctx context.Context, accountID, sourceID string, generation int64, spec EnvironmentGitSourceSpec) (EnvironmentGitSource, error) {
	if spec.Validate() != nil || spec.Mode != "report" || spec.Prune {
		return EnvironmentGitSource{}, ErrInvalidArgument
	}
	var result EnvironmentGitSource
	err := s.withGitOpsControl(ctx, accountID, sourceID, func(tx pgx.Tx, source EnvironmentGitSource) error {
		if err := detachGitSourceSQL(ctx, tx, source, generation); err != nil {
			return err
		}
		row, err := sqlc.New().CreateEnvironmentGitSource(ctx, tx, sqlc.CreateEnvironmentGitSourceParams{AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(source.ProjectID), EnvironmentSlug: source.EnvironmentSlug, RepositoryID: spec.RepositoryID, InstallationID: spec.InstallationID, Repository: spec.Repository, SourceRef: spec.Ref, ManifestPath: spec.ManifestPath, Mode: spec.Mode, ApprovalPolicy: spec.ApprovalPolicy, Prune: spec.Prune})
		if err != nil {
			return mapErr(err)
		}
		result = environmentGitSourceFromSQL(row, source.EnvironmentSlug)
		return recordGitOpsEvent(ctx, tx, result.ID, accountID, "rebound", map[string]string{"previous_source_id": sourceID})
	})
	return result, err
}
