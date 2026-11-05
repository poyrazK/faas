package state

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ProjectSharedSecretRevision tracks the whole secret set, including an empty
// set after deletion. The hash contains no secret values or value fingerprints.
type ProjectSharedSecretRevision struct {
	Hash    string
	Tracked bool
}

type ProjectSharedSecretRevisionReader interface {
	ProjectEnvironmentSharedSecretRevision(context.Context, string, string, string) (ProjectSharedSecretRevision, error)
}

func projectSharedSecretRevision(generation int64) ProjectSharedSecretRevision {
	payload := fmt.Sprintf("gregale-project-shared-secret-set-v2\n%d", generation)
	return ProjectSharedSecretRevision{Hash: fmt.Sprintf("%x", sha256.Sum256([]byte(payload))), Tracked: generation != 0}
}

func (revision ProjectSharedSecretRevision) WithWorkloads(hashes map[string]string) map[string]string {
	out := maps.Clone(hashes)
	if revision.Tracked {
		if out == nil {
			out = make(map[string]string)
		}
		out[api.ProjectSecretsRevisionKey] = revision.Hash
	}
	return out
}

func (s *PgStore) ProjectEnvironmentSharedSecretRevision(ctx context.Context, accountID, projectID, environment string) (ProjectSharedSecretRevision, error) {
	return projectEnvironmentSharedSecretRevisionDB(ctx, s.pool, accountID, projectID, environment)
}

func projectEnvironmentSharedSecretRevisionDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, environment string) (ProjectSharedSecretRevision, error) {
	row, err := sqlc.New().ProjectEnvironmentSharedSecretGeneration(ctx, db, sqlc.ProjectEnvironmentSharedSecretGenerationParams{
		AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID), EnvironmentSlug: environment,
	})
	if err != nil {
		return ProjectSharedSecretRevision{}, mapErr(err)
	}
	revision := projectSharedSecretRevision(row.SharedSecretGeneration)
	revision.Tracked = revision.Tracked || row.HasSharedSecrets
	return revision, nil
}

func (m *MemStore) ProjectEnvironmentSharedSecretRevision(_ context.Context, accountID, projectID, environment string) (ProjectSharedSecretRevision, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.projectEnvironmentSharedSecretRevisionLocked(accountID, projectID, environment)
}

func (m *MemStore) projectEnvironmentSharedSecretRevisionLocked(accountID, projectID, environment string) (ProjectSharedSecretRevision, error) {
	if err := m.projectEnvironmentOwnedLocked(accountID, projectID, environment); err != nil {
		return ProjectSharedSecretRevision{}, err
	}
	env, err := m.projectEnvironmentBySlugLocked(projectID, environment)
	if err != nil {
		return ProjectSharedSecretRevision{}, err
	}
	return projectSharedSecretRevision(m.projectSharedSecretGenerations[env.ID]), nil
}

func (m *MemStore) advanceProjectSharedSecretGenerationLocked(projectID, environment string) {
	env, err := m.projectEnvironmentBySlugLocked(projectID, environment)
	if err != nil {
		return
	}
	if m.projectSharedSecretGenerations == nil {
		m.projectSharedSecretGenerations = make(map[string]int64)
	}
	m.projectSharedSecretGenerations[env.ID]++
}

func validateQualificationSecretRevisions(expected, workloadHashes map[string]string, revision ProjectSharedSecretRevision) (map[string]string, error) {
	observed, supplied := expected[api.ProjectSecretsRevisionKey]
	workloads := maps.Clone(expected)
	delete(workloads, api.ProjectSecretsRevisionKey)
	if !sameProjectEnvironmentSecretRevisionHashes(workloads, workloadHashes) ||
		!supplied && revision.Tracked || supplied && observed != revision.Hash {
		return nil, ErrConflict
	}
	// Environments that never had shared secrets retain the N-1 representation.
	return revision.WithWorkloads(workloads), nil
}
