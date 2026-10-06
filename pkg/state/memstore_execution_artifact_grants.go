package state

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ExecutionArtifactGrantStore = (*MemStore)(nil)

func cloneExecutionArtifactGrant(grant ExecutionArtifactGrant) ExecutionArtifactGrant {
	grant.CreatorPrincipalID = cloneStringPtr(grant.CreatorPrincipalID)
	grant.TokenHash = append([]byte(nil), grant.TokenHash...)
	grant.RedeemedAt = cloneTimePtr(grant.RedeemedAt)
	grant.RedeemedExecutionID = cloneStringPtr(grant.RedeemedExecutionID)
	grant.RevokedAt = cloneTimePtr(grant.RevokedAt)
	return grant
}

func (m *MemStore) CreateExecutionArtifactGrant(_ context.Context, params CreateExecutionArtifactGrantParams) (ExecutionArtifactGrant, error) {
	if params.ID == "" || params.AccountID == "" || params.SourceExecutionID == "" || params.ArtifactName == "" || len(params.TokenHash) != 32 || !params.ExpiresAt.After(params.CreatedAt) {
		return ExecutionArtifactGrant{}, ErrExecutionInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.executions[params.SourceExecutionID]
	if !ok || row.AccountID != params.AccountID || row.Status != api.ExecutionStatusSucceeded {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	found := false
	for _, artifact := range row.Artifacts {
		if artifact.Name == params.ArtifactName {
			if err := api.ValidateExecutionArtifacts([]api.ExecutionArtifact{artifact}); err != nil {
				return ExecutionArtifactGrant{}, ErrExecutionInvalid
			}
			found = true
			break
		}
	}
	if !found {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	grant := ExecutionArtifactGrant{
		ID: params.ID, AccountID: params.AccountID, SourceExecutionID: params.SourceExecutionID,
		ArtifactName: params.ArtifactName, CreatorPrincipalID: cloneStringPtr(params.CreatorPrincipalID),
		TokenHash: append([]byte(nil), params.TokenHash...), ExpiresAt: params.ExpiresAt.UTC(), CreatedAt: params.CreatedAt.UTC(),
	}
	m.executionArtifactGrants[grant.ID] = grant
	return cloneExecutionArtifactGrant(grant), nil
}

func (m *MemStore) ExecutionArtifactGrantByToken(_ context.Context, accountID string, tokenHash []byte, at time.Time) (ExecutionArtifactGrant, error) {
	if len(tokenHash) != 32 {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, grant := range m.executionArtifactGrants {
		if grant.AccountID == accountID && grant.RevokedAt == nil && grant.RedeemedAt == nil && grant.ExpiresAt.After(at) && subtle.ConstantTimeCompare(grant.TokenHash, tokenHash) == 1 {
			return cloneExecutionArtifactGrant(grant), nil
		}
	}
	return ExecutionArtifactGrant{}, ErrNotFound
}

func (m *MemStore) RevokeExecutionArtifactGrant(_ context.Context, accountID, grantID string, principalID *string, accountWide bool, at time.Time) (ExecutionArtifactGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	grant, ok := m.executionArtifactGrants[grantID]
	if !ok || grant.AccountID != accountID || (!accountWide && (principalID == nil || grant.CreatorPrincipalID == nil || *principalID != *grant.CreatorPrincipalID)) {
		return ExecutionArtifactGrant{}, ErrNotFound
	}
	if grant.RevokedAt == nil {
		revokedAt := at.UTC()
		grant.RevokedAt = &revokedAt
		m.executionArtifactGrants[grant.ID] = grant
	}
	return cloneExecutionArtifactGrant(grant), nil
}
