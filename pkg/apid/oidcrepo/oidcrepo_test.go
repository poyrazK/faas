// adr: 20261101160300
package oidcrepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/oidcrepo"
	"github.com/onebox-faas/faas/pkg/state"
)

const issuer = "https://token.actions.githubusercontent.com"

type fakeProjects struct {
	repo     state.GitHubActionsRepository
	bindings []state.GitHubProjectRepositoryBinding
}

func (f fakeProjects) GitHubProjectBindingsForOIDCSubject(_ context.Context, issuerURL, _ string) (state.GitHubActionsRepository, []state.GitHubProjectRepositoryBinding, bool, error) {
	if issuerURL != issuer {
		return state.GitHubActionsRepository{}, nil, false, nil
	}
	return f.repo, f.bindings, true, nil
}

// repo is one repository an installation sees.
type repo struct {
	id       int64
	fullName string
}

// fakeRepos answers by installation ID; an installation absent from repos
// fails the lookup.
type fakeRepos map[int64][]repo

func (f fakeRepos) InstallationRepositoryID(_ context.Context, _ string, installationID int64, fullName string) (int64, error) {
	repos, ok := f[installationID]
	if !ok {
		return 0, errors.New("githubd unavailable")
	}
	for _, r := range repos {
		if strings.EqualFold(r.fullName, fullName) {
			return r.id, nil
		}
	}
	return 0, nil
}

func TestAccountID(t *testing.T) {
	acme := state.GitHubProjectRepositoryBinding{AccountID: "acct-acme", InstallationID: 1}
	other := state.GitHubProjectRepositoryBinding{AccountID: "acct-other", InstallationID: 2}
	sees := []repo{{42, "Acme/API"}}
	for _, tc := range []struct {
		name     string
		repo     state.GitHubActionsRepository
		bindings []state.GitHubProjectRepositoryBinding
		repos    fakeRepos
		want     string
		wantErr  bool
	}{
		{name: "installation sees the repository", repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{acme}, repos: fakeRepos{1: sees}, want: "acct-acme"},
		{name: "immutable subject names the same repository", repo: state.GitHubActionsRepository{FullName: "acme/api", RepoID: 42}, bindings: []state.GitHubProjectRepositoryBinding{acme}, repos: fakeRepos{1: sees}, want: "acct-acme"},
		{name: "immutable subject names a re-created repository", repo: state.GitHubActionsRepository{FullName: "acme/api", RepoID: 7}, bindings: []state.GitHubProjectRepositoryBinding{acme}, repos: fakeRepos{1: sees}},
		{name: "installation no longer sees the repository", repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{acme}, repos: fakeRepos{1: {{9, "acme/web"}}}},
		{name: "only the binding GitHub confirms counts", repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{acme, other}, repos: fakeRepos{1: sees, 2: nil}, want: "acct-acme"},
		{name: "two confirmed accounts are ambiguous", repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{acme, other}, repos: fakeRepos{1: sees, 2: sees}},
		{name: "githubd failure is an error", repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{acme}, repos: fakeRepos{}, wantErr: true},
		{name: "no project binds the repository", repo: state.GitHubActionsRepository{FullName: "acme/api"}, repos: fakeRepos{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := oidcrepo.Resolver{Store: fakeProjects{repo: tc.repo, bindings: tc.bindings}, Repos: tc.repos}
			got, err := resolver.AccountID(context.Background(), issuer, "repo:acme/api:ref:refs/heads/main")
			switch {
			case tc.wantErr:
				if err == nil || errors.Is(err, state.ErrNotFound) {
					t.Fatalf("err = %v, want a lookup error", err)
				}
			case tc.want == "":
				if !errors.Is(err, state.ErrNotFound) {
					t.Fatalf("AccountID = %q, %v; want ErrNotFound", got, err)
				}
			case err != nil || got != tc.want:
				t.Fatalf("AccountID = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestRepositoryID(t *testing.T) {
	resolver := oidcrepo.Resolver{
		Store: fakeProjects{repo: state.GitHubActionsRepository{FullName: "acme/api"}, bindings: []state.GitHubProjectRepositoryBinding{
			{AccountID: "acct-other", InstallationID: 2},
			{AccountID: "acct-acme", InstallationID: 1},
		}},
		Repos: fakeRepos{1: {{42, "acme/api"}}},
	}
	ctx := context.Background()
	if id, err := resolver.RepositoryID(ctx, "acct-acme", issuer, "repo:acme/api:ref:refs/heads/main"); err != nil || id != 42 {
		t.Fatalf("RepositoryID = %d, %v; want 42", id, err)
	}
	if id, err := resolver.RepositoryID(ctx, "acct-none", issuer, "repo:acme/api:ref:refs/heads/main"); err != nil || id != 0 {
		t.Fatalf("RepositoryID for an account without the project = %d, %v; want 0", id, err)
	}
	if id, err := resolver.RepositoryID(ctx, "acct-other", issuer, "repo:acme/api:ref:refs/heads/main"); err == nil {
		t.Fatalf("RepositoryID with githubd failing = %d, nil; want an error", id)
	}
	if id, err := (oidcrepo.Resolver{}).RepositoryID(ctx, "acct-acme", issuer, "repo:acme/api:ref:refs/heads/main"); err != nil || id != 0 {
		t.Fatalf("disabled resolver = %d, %v; want 0", id, err)
	}
}
