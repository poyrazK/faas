package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/gitlabsource"
	"github.com/onebox-faas/faas/pkg/state"
)

// companyProjectGitLabUnavailable refuses a GitLab source for a company
// project: a GitLab connection belongs to one member, and a company project
// never acts through a member's own credentials, as for GitHub
// (ADR-20261110200000). A company project's GitOps uses the project's GitHub
// repository (ADR-20261112230000).
func companyProjectGitLabUnavailable() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeGitLabCompanyProjectUnavailable, "GitLab unavailable for company projects",
		"a GitLab connection belongs to one member; use the company project's GitHub repository instead")
}

func (s *server) environmentGitSourceSpecForRequest(ctx context.Context, account state.Account, project state.Project, request api.CreateEnvironmentGitSourceRequest) (state.EnvironmentGitSourceSpec, *api.Problem) {
	spec := state.EnvironmentGitSourceSpec{Provider: request.Provider, Ref: request.Ref,
		ManifestPath: request.ManifestPath, Mode: request.Mode, ApprovalPolicy: request.ApprovalPolicy, Prune: request.Prune}
	if spec.Provider == "" {
		spec.Provider = "github"
	}
	switch spec.Provider {
	case "github":
		if request.Repository != "" && request.Repository != project.RepoFullName {
			return spec, api.ErrValidation("GitHub source must use the project's bound repository.")
		}
		repositoryID, err := s.verifyEnvironmentGitRepository(ctx, account.ID, project.InstallID, project.RepoFullName, 0)
		if err != nil {
			return spec, api.ErrSourceRefUnavailable("Could not verify the GitHub repository.")
		}
		spec.RepositoryID, spec.InstallationID, spec.Repository = repositoryID, project.InstallID, project.RepoFullName
	case "gitlab":
		if _, shared := projectaccess.From(ctx); shared {
			return spec, companyProjectGitLabUnavailable()
		}
		if request.Ref == "" || !gitlabsource.ValidRepositoryPath(request.Repository) {
			return spec, api.ErrValidation("GitLab.com GitOps requires a repository path and explicit ref.")
		}
		token, err := s.gitLabTokenForAccount(ctx, account.ID)
		if err != nil {
			return spec, api.NewProblem(http.StatusConflict, "gitlab_connection_required", "GitLab connection required", "Connect a GitLab.com account before binding this source.")
		}
		repository, err := s.gitLabClient().VerifyRepository(ctx, token, request.Repository, 0)
		if err != nil {
			return spec, api.ErrSourceRefUnavailable("Could not verify this GitLab repository under the connected account.")
		}
		spec.RepositoryID, spec.Repository = repository.ID, repository.Path
	default:
		return spec, api.ErrValidation("Unsupported Git source provider.")
	}
	if err := spec.Validate(); err != nil {
		return spec, api.ErrValidation(err.Error())
	}
	return spec, nil
}
