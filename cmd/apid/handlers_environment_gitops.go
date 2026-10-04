package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func writeEnvironmentGitOpsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "environment_git_source_not_found", "Git source not found", "No environment Git source or managed field exists in this scope."))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_stale_plan", "Environment state changed", "Read the current source and review a fresh plan before retrying."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("Invalid environment GitOps request."))
	default:
		api.WriteProblem(w, api.ErrCapacity("Environment GitOps is temporarily unavailable."))
	}
}

func (s *server) environmentGitSourceForRequest(r *http.Request, acct state.Account) (state.EnvironmentGitSource, *api.Problem) {
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, r.PathValue("slug"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return state.EnvironmentGitSource{}, projectNotFound(r.PathValue("slug"))
		}
		return state.EnvironmentGitSource{}, api.ErrCapacity("Could not read the project.")
	}
	store, ok := s.store.(state.EnvironmentGitOpsStore)
	if !ok {
		return state.EnvironmentGitSource{}, api.ErrCapacity("Environment GitOps storage is unavailable.")
	}
	source, err := store.EnvironmentGitSource(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return source, api.NewProblem(http.StatusNotFound, "environment_git_source_not_found", "Git source not found", "No Git source is bound to this project environment.")
		}
		return source, api.ErrCapacity("Could not read the environment Git source.")
	}
	return source, nil
}

func (s *server) verifyEnvironmentGitRepository(ctx context.Context, accountID string, installationID int64, repository string, expectedID int64) (int64, error) {
	if installationID <= 0 || repository == "" {
		return 0, state.ErrInvalidArgument
	}
	repositories, err := s.githubd.ListInstallableRepos(ctx, accountID, installationID)
	if err != nil {
		return 0, err
	}
	for _, candidate := range repositories {
		if candidate.FullName == repository && candidate.ID > 0 && (expectedID == 0 || candidate.ID == expectedID) {
			return candidate.ID, nil
		}
	}
	return 0, state.ErrNotFound
}

func (s *server) createEnvironmentGitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, environment, _, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.CreateEnvironmentGitSourceRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid Git source request."))
		return
	}
	if request.Mode == "" {
		request.Mode = "report"
	}
	if rejectEnvironmentGitEnforcement(w, request.Mode) {
		return
	}
	if request.ApprovalPolicy == "" {
		request.ApprovalPolicy = "manual"
	}
	if request.Ref == "" {
		request.Ref = "refs/heads/" + project.ProductionBranch
	}
	repositoryID, err := s.verifyEnvironmentGitRepository(r.Context(), acct.ID, project.InstallID, project.RepoFullName, 0)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable store"))
		return
	}
	source, err := store.CreateEnvironmentGitSource(r.Context(), acct.ID, project.ID, environment.Slug, state.EnvironmentGitSourceSpec{
		RepositoryID: repositoryID, InstallationID: project.InstallID, Repository: project.RepoFullName,
		Ref: request.Ref, ManifestPath: request.ManifestPath, Mode: request.Mode, ApprovalPolicy: request.ApprovalPolicy, Prune: request.Prune,
	})
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "project.environment.git_source.created", &acct.ID, map[string]any{"source_id": source.ID, "environment_id": source.EnvironmentID, "repository_id": repositoryID})
	writeJSON(w, http.StatusCreated, source)
}

func (s *server) readEnvironmentGitRevision(ctx context.Context, acct state.Account, source state.EnvironmentGitSource, commitSHA string) (environmentsync.DesiredState, *api.Problem) {
	if !isCanonicalCommitSHA(commitSHA) {
		return environmentsync.DesiredState{}, api.ErrValidation("commit_sha must be an immutable lowercase GitHub commit SHA.")
	}
	if _, err := s.verifyEnvironmentGitRepository(ctx, acct.ID, source.Spec.InstallationID, source.Spec.Repository, source.Spec.RepositoryID); err != nil {
		return environmentsync.DesiredState{}, api.ErrSourceRefUnavailable("Could not verify the bound repository identity.")
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		return environmentsync.DesiredState{}, api.ErrCapacity("Plan limits are unavailable.")
	}
	maxBytes := int64(limits.SourceTarballMaxMB) << 20
	stream, err := s.githubd.StreamSourceRef(ctx, acct.ID, source.Spec.InstallationID, source.Spec.Repository, commitSHA, maxBytes)
	if err != nil || stream == nil || stream.Body == nil {
		return environmentsync.DesiredState{}, api.ErrSourceRefUnavailable("Could not fetch the reviewed Git revision.")
	}
	desired, parseErr := environmentgitops.ReadGitDefinition(stream.Body, source.Spec.ManifestPath, maxBytes)
	closeErr := stream.Body.Close()
	if closeErr != nil || stream.Stats == nil || stream.Stats.Err != nil || stream.Stats.Truncated || stream.Stats.ResolvedCommitSHA != commitSHA {
		return environmentsync.DesiredState{}, api.ErrSourceRefUnavailable("The Git revision stream could not be verified.")
	}
	if parseErr != nil {
		return environmentsync.DesiredState{}, api.ErrValidation("The Git revision does not contain a valid environment definition.")
	}
	project, err := s.store.ProjectByID(ctx, source.ProjectID)
	if err != nil {
		return environmentsync.DesiredState{}, api.ErrCapacity("Could not verify the project scope.")
	}
	if project.AccountID != acct.ID || desired.Definition.Project != project.Slug || desired.Definition.Environment != source.EnvironmentSlug {
		return environmentsync.DesiredState{}, api.ErrValidation("The definition targets a different project or environment.")
	}
	return desired, nil
}

func (s *server) previewEnvironmentGitRevision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.PreviewEnvironmentGitRevisionRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid revision request."))
		return
	}
	desired, problem := s.readEnvironmentGitRevision(r.Context(), acct, source, request.CommitSHA)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	raw, _ := json.Marshal(desired.Definition)
	writeJSON(w, http.StatusOK, api.PreviewEnvironmentGitRevisionResponse{CommitSHA: request.CommitSHA, DefinitionDigest: desired.Digest, Definition: raw, Generation: source.Generation})
}

func (s *server) approveEnvironmentGitRevision(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if source.Spec.ApprovalPolicy != "manual" {
		api.WriteProblem(w, api.ErrValidation("This source requires verified protected-branch merge approval."))
		return
	}
	var request api.ApproveEnvironmentGitRevisionRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid revision approval."))
		return
	}
	if request.ExpectedGeneration != source.Generation {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	desired, problem := s.readEnvironmentGitRevision(r.Context(), acct, source, request.CommitSHA)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if request.DefinitionDigest != desired.Digest {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	current, revision, err := s.store.(state.EnvironmentGitOpsStore).ApproveEnvironmentDesiredRevision(r.Context(), state.ApproveEnvironmentRevision{AccountID: acct.ID, SourceID: source.ID, ExpectedGeneration: request.ExpectedGeneration, CommitSHA: request.CommitSHA, Desired: desired, ApprovedBy: acct.ID})
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, api.ApproveEnvironmentGitRevisionResponse{Source: current, Revision: revision})
}

func (s *server) getEnvironmentGitOps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	runs, err := s.store.(state.EnvironmentGitOpsStore).ListEnvironmentGitOpsRuns(r.Context(), acct.ID, source.ID, 20)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	if runs == nil {
		runs = []state.EnvironmentGitOpsRun{}
	}
	response := api.EnvironmentGitOpsStatusResponse{Source: source, Runs: runs}
	response.Approval, err = s.environmentGitApprovalForSource(r.Context(), source)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) previewEnvironmentGitOpsAdoption(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsIntentStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable intent store"))
		return
	}
	plan, err := store.PreviewEnvironmentGitOpsAdoption(r.Context(), acct.ID, source.ID)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *server) adoptEnvironmentGitOps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.AdoptEnvironmentGitOpsRequest
	if err := decodeJSON(r, &request); err != nil || len(request.PlanHash) != 64 {
		api.WriteProblem(w, api.ErrValidation("A reviewed plan_hash is required."))
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsIntentStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable intent store"))
		return
	}
	if err := store.AdoptEnvironmentGitOps(r.Context(), acct.ID, source.ID, request.PlanHash); err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, api.AdoptEnvironmentGitOpsResponse{Status: "adopted"})
}

func (s *server) updateEnvironmentGitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request state.EnvironmentGitSourceUpdate
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid source controls."))
		return
	}
	if rejectEnvironmentGitEnforcement(w, request.Mode) {
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsControlStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable controls"))
		return
	}
	current, err := store.UpdateEnvironmentGitSource(r.Context(), acct.ID, source.ID, request)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, current)
}

func (s *server) createEnvironmentGitOpsOverride(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request state.EnvironmentGitOpsOverrideRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid temporary override."))
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsControlStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable controls"))
		return
	}
	if err := store.SetEnvironmentGitOpsOverride(r.Context(), acct.ID, source.ID, request); err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, request)
}

func (s *server) removeEnvironmentGitOpsOverride(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.RemoveEnvironmentGitOpsOverrideRequest
	if err := decodeJSON(r, &request); err != nil || strings.TrimSpace(request.Resource) == "" || strings.TrimSpace(request.Path) == "" {
		api.WriteProblem(w, api.ErrValidation("resource and path are required."))
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsControlStore)
	if !ok {
		writeEnvironmentGitOpsError(w, errors.New("unavailable controls"))
		return
	}
	if err := store.RemoveEnvironmentGitOpsOverride(r.Context(), acct.ID, source.ID, request.Resource, request.Path); err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) environmentGitApprovalForSource(ctx context.Context, source state.EnvironmentGitSource) (*api.EnvironmentGitRevisionApproval, error) {
	if source.ApprovedRevisionID == "" || source.Spec.ApprovalPolicy != "protected_branch" {
		return nil, nil
	}
	store, ok := s.store.(state.EnvironmentGitApprovalStore)
	if !ok {
		return nil, errors.New("approval storage unavailable")
	}
	record, err := store.EnvironmentGitRevisionApproval(ctx, source.AccountID, source.ID, source.ApprovedRevisionID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &record, nil
}
