package e2e_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest/gitlabfixture"
	"github.com/onebox-faas/faas/pkg/state"
)

type gitOpsSourceProvider interface {
	add(*testing.T, string, api.EnvironmentDefinition)
	addFiles(*testing.T, string, api.EnvironmentDefinition, map[string][]byte)
	setUnavailable(bool)
	outageCode() string
}

func (*gitOpsSourceFixture) outageCode() string { return "environment_git_source_unavailable" }

type gitLabGitOpsSourceFixture struct{ fixture *gitlabfixture.Fixture }

func (f *gitLabGitOpsSourceFixture) add(t *testing.T, sha string, definition api.EnvironmentDefinition) {
	f.fixture.Add(t, sha, definition)
}
func (f *gitLabGitOpsSourceFixture) addFiles(t *testing.T, sha string, definition api.EnvironmentDefinition, files map[string][]byte) {
	f.fixture.AddFiles(t, sha, definition, files)
}
func (f *gitLabGitOpsSourceFixture) setUnavailable(value bool) { f.fixture.SetUnavailable(value) }
func (*gitLabGitOpsSourceFixture) outageCode() string {
	return "environment_git_repository_unavailable"
}

func newGitOpsProviderFixture(t *testing.T, provider string) (gitOpsSourceProvider, []string) {
	t.Helper()
	if provider == "gitlab" {
		f := gitlabfixture.New(t)
		return &gitLabGitOpsSourceFixture{fixture: f}, f.Env()
	}
	if provider != "github" {
		t.Fatalf("unsupported fixture provider %q", provider)
	}
	f := newGitOpsSourceFixture(t)
	return f, []string{"FAAS_GITHUBD_SOCKET=" + f.socket}
}

func connectGitOpsProvider(t *testing.T, provider gitOpsSourceProvider, store state.GitLabOAuthStore, accountID string) {
	t.Helper()
	switch f := provider.(type) {
	case *gitOpsSourceFixture:
		f.setAccount(accountID)
	case *gitLabGitOpsSourceFixture:
		sealed, expires := f.fixture.SealedTokens(t)
		if _, err := store.PutGitLabOAuthConnection(t.Context(), state.GitLabOAuthConnection{
			AccountID: accountID, GitLabUserID: 41, Username: "acceptance-owner", CredentialsSealed: sealed, ExpiresAt: expires,
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func gitOpsProviderProject(provider, slug, accountID string) state.Project {
	project := state.Project{AccountID: accountID, Slug: slug, ProductionBranch: "main"}
	if provider == "github" {
		project.RepoFullName, project.InstallID = "example/shop", 42
	}
	return project
}
