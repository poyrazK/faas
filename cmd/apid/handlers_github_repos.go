package main

// Customer-facing GitHub repository discovery for bearer/API-key callers.
// The dashboard has a session-bound picker at /v1/install/repos/list; this
// route exposes the same account-owned catalog to CI and the CLI without
// requiring callers to copy an installation id out of a browser response.

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// listGitHubRepositories lists repositories visible to the account's durable
// GitHub App installation. The installation id is deliberately resolved from
// the account row server-side: callers never need to guess or hand-copy an
// installation id, and a bearer token cannot enumerate another account's
// installation.
func (s *server) listGitHubRepositories(w http.ResponseWriter, r *http.Request, acct state.Account) {
	install, err := s.store.GitHubInstallForAccount(r.Context(), acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrGitHubInstallNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read GitHub installation"))
		return
	}

	repos, err := s.githubd.ListInstallableRepos(r.Context(), acct.ID, install.InstallationID)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadGateway, "github_unreachable",
			"Could not reach GitHub", "retry in a minute"))
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
