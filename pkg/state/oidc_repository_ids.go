package state

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// OIDCRepositoryIDResolver returns the immutable GitHub owner and repository
// IDs an account's app binding recorded for an OIDC subject's repository.
type OIDCRepositoryIDResolver interface {
	GitHubRepositoryIDsForOIDCSubject(ctx context.Context, accountID, issuerURL, subject string) (ownerID, repoID int64, err error)
}

var (
	_ OIDCRepositoryIDResolver = (*PgStore)(nil)
	_ OIDCRepositoryIDResolver = (*MemStore)(nil)
)

// GitHubRepositoryIDsForOIDCSubject returns the immutable owner and
// repository IDs accountID's app binding recorded for the repository a GitHub
// Actions subject names, or zeros when the subject is not a GitHub Actions
// repository subject or no binding of that name records them. The OIDC
// exchange requires a token's repository_owner_id and repository_id claims to
// equal them, so a repository recreated under a freed owner name does not
// pass (bug hunt 2026-10-28 H68).
func (s *PgStore) GitHubRepositoryIDsForOIDCSubject(ctx context.Context, accountID, issuerURL, subject string) (ownerID, repoID int64, err error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok {
		return 0, 0, nil
	}
	id, err := uuid.Parse(accountID)
	if err != nil {
		return 0, 0, ErrNotFound
	}
	row, err := sqlc.New().GitHubRepositoryIDsBoundToAccount(ctx, s.pool, sqlc.GitHubRepositoryIDsBoundToAccountParams{
		AccountID: pgtype.UUID{Bytes: id, Valid: true}, RepoFullName: identity.FullName,
	})
	if err != nil {
		return 0, 0, err
	}
	return row.OwnerID, row.RepoID, nil
}

// GitHubRepositoryIDsForOIDCSubject implements the same lookup over the
// in-memory GitHub bindings.
func (m *MemStore) GitHubRepositoryIDsForOIDCSubject(_ context.Context, accountID, issuerURL, subject string) (ownerID, repoID int64, err error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok {
		return 0, 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for appID, binding := range m.githubBindings {
		app, found := m.apps[appID]
		if !found || app.AccountID != accountID || app.Status == AppDeleted || !strings.EqualFold(binding.RepoFullName, identity.FullName) {
			continue
		}
		ownerID, repoID = max(ownerID, binding.OwnerID), max(repoID, binding.RepoID)
	}
	return ownerID, repoID, nil
}
