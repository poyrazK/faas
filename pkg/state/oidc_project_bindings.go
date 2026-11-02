package state

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GitHubActionsRepository is the repository a GitHub Actions OIDC subject
// names. OwnerID and RepoID are set only for GitHub's immutable subject
// format.
type GitHubActionsRepository struct {
	FullName string
	OwnerID  int64
	RepoID   int64
}

// GitHubProjectRepositoryBinding is a project bound to a GitHub repository
// through an installation its account connected.
type GitHubProjectRepositoryBinding struct {
	AccountID      string
	InstallationID int64
}

// OIDCProjectRepositoryResolver lists the projects bound to the repository a
// GitHub Actions subject names (ADR-20261101160300, amendment 1). A project
// records no immutable repository ID, so the caller confirms each binding
// with GitHub before trusting it. ok is false when the subject is not a
// GitHub Actions repository subject.
type OIDCProjectRepositoryResolver interface {
	GitHubProjectBindingsForOIDCSubject(ctx context.Context, issuerURL, subject string) (repo GitHubActionsRepository, bindings []GitHubProjectRepositoryBinding, ok bool, err error)
}

var (
	_ OIDCProjectRepositoryResolver = (*PgStore)(nil)
	_ OIDCProjectRepositoryResolver = (*MemStore)(nil)
)

// GitHubProjectBindingsForOIDCSubject implements OIDCProjectRepositoryResolver.
func (s *PgStore) GitHubProjectBindingsForOIDCSubject(ctx context.Context, issuerURL, subject string) (GitHubActionsRepository, []GitHubProjectRepositoryBinding, bool, error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok {
		return GitHubActionsRepository{}, nil, false, nil
	}
	rows, err := sqlc.New().GitHubProjectBindingsForRepository(ctx, s.pool, identity.FullName)
	if err != nil {
		return GitHubActionsRepository{}, nil, false, err
	}
	bindings := make([]GitHubProjectRepositoryBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, GitHubProjectRepositoryBinding{AccountID: row.AccountID, InstallationID: row.InstallationID})
	}
	return GitHubActionsRepository(identity), bindings, true, nil
}

// GitHubProjectBindingsForOIDCSubject implements the same lookup over the
// in-memory projects.
func (m *MemStore) GitHubProjectBindingsForOIDCSubject(_ context.Context, issuerURL, subject string) (GitHubActionsRepository, []GitHubProjectRepositoryBinding, bool, error) {
	identity, ok := githubActionsRepositoryIdentityFromSubject(issuerURL, subject)
	if !ok {
		return GitHubActionsRepository{}, nil, false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var bindings []GitHubProjectRepositoryBinding
	for _, p := range m.projects {
		if p.InstallID <= 0 || !strings.EqualFold(p.RepoFullName, identity.FullName) {
			continue
		}
		if _, connected := m.githubInstalls[p.AccountID+"\x00"+strconv.FormatInt(p.InstallID, 10)]; !connected {
			continue
		}
		bindings = append(bindings, GitHubProjectRepositoryBinding{AccountID: p.AccountID, InstallationID: p.InstallID})
	}
	sort.Slice(bindings, func(i, j int) bool {
		if bindings[i].AccountID != bindings[j].AccountID {
			return bindings[i].AccountID < bindings[j].AccountID
		}
		return bindings[i].InstallationID < bindings[j].InstallationID
	})
	return GitHubActionsRepository(identity), bindings, true, nil
}
