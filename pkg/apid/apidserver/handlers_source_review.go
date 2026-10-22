package apidserver

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var sourceReviewRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9._-]*$`)

func (s *server) reviewSourceRef(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var input api.SourceReviewRequest
	if decodeJSON(r, &input) != nil || !sourceReviewRepo.MatchString(input.Repo) || len(input.Repo) > 200 || !isValidRef(input.Ref) || strings.HasPrefix(input.Ref, "refs/tags/") {
		api.WriteProblem(w, api.ErrValidation("provide a connected GitHub owner/repository and a branch or full lowercase commit SHA"))
		return
	}
	request := r.Clone(r.Context())
	copyURL := *r.URL
	query := copyURL.Query()
	query.Set("environment", input.Environment)
	copyURL.RawQuery = query.Encode()
	request.URL = &copyURL
	app, env, problem := s.appEnvironmentSettings(w, request, acct, app, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if app.Manifest.ExecutionMode != "" && app.Manifest.ExecutionMode != api.ExecutionModeRequest && app.Manifest.ExecutionMode != api.ExecutionModeService && app.Manifest.ExecutionMode != api.ExecutionModeWorker {
		api.WriteProblem(w, api.ErrValidation("reviewed source releases support request, service and worker modes"))
		return
	}
	result, problem := s.resolveSourceReview(r.Context(), acct, app, input)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	scope := api.DefaultEnvScope
	if env.ID != "" {
		scope = env.Slug
	}
	store, ok := s.store.(state.ReleaseReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release review is unavailable"))
		return
	}
	review, err := store.ReadReleaseReview(r.Context(), app.ID, scope)
	if err != nil {
		s.writeDeploymentCreateError(w, err)
		return
	}
	result.ReleaseReview = review
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func (s *server) resolveSourceReview(ctx context.Context, acct state.Account, app state.App, input api.SourceReviewRequest) (api.SourceReviewResponse, *api.Problem) {
	var result api.SourceReviewResponse
	install, problem := s.resolveInstallToken(ctx, acct, app, input.Repo)
	if problem != nil {
		return result, problem
	}
	repository, problem := s.sourceReviewRepository(ctx, acct.ID, install, input.Repo)
	if problem != nil {
		return result, problem
	}
	commit := input.Ref
	if !isCanonicalCommitSHA(commit) {
		branches, ok := s.githubd.(interface {
			GetBranchHead(context.Context, string, int64, string, string) (string, bool, error)
		})
		if !ok {
			return result, api.ErrSourceRefUnavailable("branch resolution is unavailable")
		}
		sha, found, err := branches.GetBranchHead(ctx, acct.ID, install, input.Repo, strings.TrimPrefix(input.Ref, "refs/heads/"))
		if err != nil {
			return result, api.ErrSourceRefUnavailable("could not resolve the GitHub branch")
		}
		if !found || !isCanonicalCommitSHA(sha) {
			return result, api.ErrValidation("ref must resolve to a branch or be a full lowercase commit SHA; use the commit SHA for tags")
		}
		commit = sha
	}
	return api.SourceReviewResponse{AppID: app.ID, Repo: repository.FullName, CommitSHA: commit, InstallationID: install, RepositoryID: repository.ID, SourceRoot: app.RootDir}, nil
}

func (s *server) sourceReviewRepository(ctx context.Context, account string, install int64, name string) (Repo, *api.Problem) {
	repos, err := s.githubd.ListInstallableRepos(ctx, account, install)
	if err != nil {
		return Repo{}, api.ErrSourceRefUnavailable("could not verify the reviewed repository")
	}
	for _, repo := range repos {
		if canonicalGitHubRepo(repo.FullName) == canonicalGitHubRepo(name) && repo.ID > 0 {
			return repo, nil
		}
	}
	return Repo{}, api.NewProblem(409, api.CodeSourceRefStale, "Repository changed", "the reviewed GitHub repository is no longer accessible through its installation")
}

func (s *server) checkReviewedSource(ctx context.Context, acct state.Account, app state.App, req api.SourceRefDeployRequest, install int64) *api.Problem {
	if req.ExpectedSource == nil {
		return nil
	}
	expected := req.ExpectedSource
	repository, problem := s.sourceReviewRepository(ctx, acct.ID, install, req.Repo)
	if problem != nil {
		return problem
	}
	if expected.AppID != app.ID || expected.SourceRoot != app.RootDir || expected.InstallationID != install || expected.RepositoryID != repository.ID || canonicalGitHubRepo(expected.Repo) != canonicalGitHubRepo(req.Repo) || expected.CommitSHA != req.Ref {
		return api.ErrSourceRefStale("repository, installation, app or source root changed after review")
	}
	return nil
}

func reviewedSourceStage(req api.SourceRefDeployRequest, rollout *api.CreateDeploymentRequest) *api.Problem {
	if req.ExpectedSource == nil {
		if req.NativeRollout != "" {
			return api.ErrValidation("native_rollout requires expected_source")
		}
		return nil
	}
	rollout.NativeRollout = req.NativeRollout
	if !isCanonicalCommitSHA(req.Ref) || req.SourceBranch != "" || !req.NoTriggers || req.Canary != nil || req.Healthcheck != nil || req.RollbackOn5xx != nil || req.DisableStartupCPUBoost != nil {
		return api.ErrValidation("reviewed source staging requires an immutable commit, no declaration changes, and no rollout overrides")
	}
	rollout.ExpectedReview = &req.ExpectedSource.ReleaseReview
	if req.Environment == "" {
		rollout.Scope = api.DefaultEnvScope
	}
	return validateReviewedStage(*rollout)
}
