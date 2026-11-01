package apidserver

// Customer-facing GitHub repository discovery for bearer/API-key callers.
// The dashboard has a session-bound picker at /v1/install/repos/list; this
// route exposes the same account-owned catalog to CI and the CLI without
// requiring callers to copy an installation id out of a browser response.

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// listGitHubRepositories lists repositories visible to one of the account's
// durable GitHub App installations. The installation_id query parameter
// names it; without one the account's only installation is used, and an
// account with several gets a 409 listing them (ADR-20261101160300). A
// bearer token cannot enumerate another account's installation.
func (s *server) listGitHubRepositories(w http.ResponseWriter, r *http.Request, acct state.Account) {
	install, problem := s.accountGitHubInstallation(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}

	repos, err := s.githubd.ListInstallableRepos(r.Context(), acct.ID, install.InstallationID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, githubUnreachableProblem("Retry in a minute."))
		return
	}

	out := make([]api.RepoResponse, 0, len(repos))
	for _, repo := range repos {
		out = append(out, api.RepoResponse{
			ID:            repo.ID,
			FullName:      repo.FullName,
			DefaultBranch: repo.DefaultBranch,
			Private:       repo.Private,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// accountGitHubInstallation resolves the installation a bearer request
// names with ?installation_id=, or the account's only one.
func (s *server) accountGitHubInstallation(r *http.Request, acct state.Account) (state.GitHubInstall, *api.Problem) {
	if raw := strings.TrimSpace(r.URL.Query().Get("installation_id")); raw != "" {
		installationID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || installationID <= 0 {
			return state.GitHubInstall{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid installation_id", "installation_id must be a positive integer")
		}
		install, err := s.store.GitHubInstallForAccountInstallation(r.Context(), acct.ID, installationID)
		if errors.Is(err, state.ErrNotFound) {
			return state.GitHubInstall{}, api.NewProblem(http.StatusForbidden, api.CodeGitHubInstallNotOwned,
				"GitHub installation is not connected to this account", "connect the GitHub App before listing its repositories")
		}
		if err != nil {
			return state.GitHubInstall{}, api.ErrCapacity("could not read GitHub installation")
		}
		return install, nil
	}
	installs, err := s.store.ListGitHubInstallationsForAccount(r.Context(), acct.ID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.GitHubInstall{}, api.ErrCapacity("could not read GitHub installation")
	}
	switch len(installs) {
	case 0:
		return state.GitHubInstall{}, api.ErrGitHubInstallNotFound()
	case 1:
		return installs[0], nil
	}
	ids := make([]int64, 0, len(installs))
	for _, install := range installs {
		ids = append(ids, install.InstallationID)
	}
	return state.GitHubInstall{}, api.ErrGitHubInstallationRequired(ids)
}
