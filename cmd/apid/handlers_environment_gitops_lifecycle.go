package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) detachEnvironmentGitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.DetachEnvironmentGitSourceRequest
	if decodeJSON(r, &request) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid ownership release request."))
		return
	}
	store, ok := s.store.(state.EnvironmentGitOpsLifecycleStore)
	if !ok {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	if err := store.DetachEnvironmentGitSource(r.Context(), acct.ID, source.ID, request.ExpectedGeneration); err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "project.environment.git_source.detached", &acct.ID, map[string]any{"source_id": source.ID, "generation": request.ExpectedGeneration})
	w.WriteHeader(http.StatusNoContent)
}
func (s *server) rebindEnvironmentGitSource(w http.ResponseWriter, r *http.Request, acct state.Account) {
	source, problem := s.environmentGitSourceForRequest(r, acct)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.RebindEnvironmentGitSourceRequest
	if decodeJSON(r, &request) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid source rebinding request."))
		return
	}
	if source.Generation != request.ExpectedGeneration {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	project, err := s.store.ProjectByID(r.Context(), source.ProjectID)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	repoID, err := s.verifyEnvironmentGitRepository(r.Context(), acct.ID, project.InstallID, project.RepoFullName, 0)
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	if request.Ref == "" {
		request.Ref = "refs/heads/" + project.ProductionBranch
	}
	if request.ApprovalPolicy == "" {
		request.ApprovalPolicy = "manual"
	}
	s.rebindVerifiedEnvironmentGitSource(w, r, acct, source, request, project, repoID)
}

func (s *server) rebindVerifiedEnvironmentGitSource(w http.ResponseWriter, r *http.Request, acct state.Account, source state.EnvironmentGitSource, request api.RebindEnvironmentGitSourceRequest, project state.Project, repoID int64) {
	store, ok := s.store.(state.EnvironmentGitOpsLifecycleStore)
	if !ok {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	next, err := store.RebindEnvironmentGitSource(r.Context(), acct.ID, source.ID, request.ExpectedGeneration, state.EnvironmentGitSourceSpec{RepositoryID: repoID, InstallationID: project.InstallID, Repository: project.RepoFullName, Ref: request.Ref, ManifestPath: request.ManifestPath, ApprovalPolicy: request.ApprovalPolicy, Mode: "report"})
	if err != nil {
		writeEnvironmentGitOpsError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "project.environment.git_source.rebound", &acct.ID, map[string]any{"source_id": next.ID, "previous_source_id": source.ID})
	writeJSON(w, http.StatusCreated, next)
}
func (s *server) claimEnvironmentFieldOwnership(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.environmentFieldOwnership(w, r, acct, false)
}
func (s *server) releaseEnvironmentFieldOwnership(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.environmentFieldOwnership(w, r, acct, true)
}
func (s *server) environmentFieldOwnership(w http.ResponseWriter, r *http.Request, acct state.Account, release bool) {
	var request api.EnvironmentFieldOwnershipRequest
	if decodeJSON(r, &request) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid field ownership request."))
		return
	}
	store, ok := s.store.(state.EnvironmentFieldOwnershipStore)
	if !ok {
		writeEnvironmentGitOpsError(w, state.ErrConflict)
		return
	}
	managed, err := store.SetEnvironmentFieldOwnership(r.Context(), acct.ID, request, release)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_field_ownership_conflict", "Field ownership conflict", "These fields are owned by Git. Release their Git ownership before managing them with Terraform."))
			return
		}
		writeEnvironmentGitOpsError(w, err)
		return
	}
	status := "unscoped"
	if managed {
		status = "claimed"
		if release {
			status = "released"
		}
	}
	s.audit.Emit(r.Context(), "environment.field_ownership."+status, &acct.ID, map[string]any{"app": request.App, "project": request.Project, "environment": request.Environment, "manager": request.Manager, "paths": request.Paths})
	writeJSON(w, http.StatusOK, api.EnvironmentFieldOwnershipResponse{Status: status})
}
