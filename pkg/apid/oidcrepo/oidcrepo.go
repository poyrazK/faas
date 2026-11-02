// Package oidcrepo lets a GitHub Actions workflow of a repository connected
// only through a project exchange its OIDC token for a Gregale deploy bearer
// (ADR-20261101160300, amendment 1).
//
// An app binding records the repository's immutable GitHub IDs; a project
// records only the repository name and installation. Trusting the name alone
// would let a repository re-created under a freed owner name deploy into the
// account (bug hunt 2026-10-28 H68), so every project binding is confirmed
// with GitHub: the binding counts only while its installation sees a
// repository of that name, and the exchange then requires the token's
// repository_id claim to equal that repository's ID.
package oidcrepo

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// Repo is one repository an installation can see.
type Repo struct {
	ID       int64
	FullName string
}

// RepoLister lists the repositories an account's installation can see.
// githubd answers it; only githubd calls GitHub.
type RepoLister interface {
	ListInstallableRepos(ctx context.Context, accountID string, installationID int64) ([]Repo, error)
}

// Resolver confirms project repository bindings with GitHub.
type Resolver struct {
	Store state.OIDCProjectRepositoryResolver
	Repos RepoLister
}

// Enabled reports whether the resolver has both its store and GitHub access.
func (r Resolver) Enabled() bool { return r.Store != nil && r.Repos != nil }

// AccountID returns the one account whose project binds the subject's
// repository through an installation that sees it. state.ErrNotFound when no
// binding is confirmed, or bindings of more than one account are.
func (r Resolver) AccountID(ctx context.Context, issuerURL, subject string) (string, error) {
	if !r.Enabled() {
		return "", state.ErrNotFound
	}
	repo, bindings, ok, err := r.Store.GitHubProjectBindingsForOIDCSubject(ctx, issuerURL, subject)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", state.ErrNotFound
	}
	accounts := map[string]struct{}{}
	var lookupErr error
	for _, binding := range bindings {
		id, err := r.repositoryID(ctx, binding, repo.FullName)
		if err != nil {
			lookupErr = err
			continue
		}
		if id == 0 || repo.RepoID != 0 && id != repo.RepoID {
			continue
		}
		accounts[binding.AccountID] = struct{}{}
	}
	if len(accounts) == 1 {
		for accountID := range accounts {
			return accountID, nil
		}
	}
	if len(accounts) == 0 && lookupErr != nil {
		return "", lookupErr
	}
	return "", state.ErrNotFound
}

// RepositoryID returns the GitHub ID of the subject's repository as
// accountID's project installation sees it, or 0 when no project of the
// account binds a repository of that name its installation sees.
func (r Resolver) RepositoryID(ctx context.Context, accountID, issuerURL, subject string) (int64, error) {
	if !r.Enabled() {
		return 0, nil
	}
	repo, bindings, ok, err := r.Store.GitHubProjectBindingsForOIDCSubject(ctx, issuerURL, subject)
	if err != nil || !ok {
		return 0, err
	}
	for _, binding := range bindings {
		if binding.AccountID != accountID {
			continue
		}
		id, err := r.repositoryID(ctx, binding, repo.FullName)
		if err != nil {
			return 0, err
		}
		if id != 0 {
			return id, nil
		}
	}
	return 0, nil
}

func (r Resolver) repositoryID(ctx context.Context, binding state.GitHubProjectRepositoryBinding, fullName string) (int64, error) {
	repos, err := r.Repos.ListInstallableRepos(ctx, binding.AccountID, binding.InstallationID)
	if err != nil {
		return 0, fmt.Errorf("oidcrepo: list repositories of installation %d: %w", binding.InstallationID, err)
	}
	for _, repo := range repos {
		if repo.ID > 0 && strings.EqualFold(repo.FullName, fullName) {
			return repo.ID, nil
		}
	}
	return 0, nil
}
