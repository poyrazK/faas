package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createProjectEnvironmentQualification(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := strings.TrimSpace(r.PathValue("slug"))
	environment := strings.TrimSpace(r.PathValue("environment"))
	if !api.ValidProjectSlug(projectSlug) || !api.ValidProjectEnvironmentSlug(environment) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid project environment", "project and environment slugs must be valid"))
		return
	}
	var request api.CreateProjectEnvironmentQualificationRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, projectSlug)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, projectNotFound(projectSlug))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project"))
		return
	}
	if _, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environment); errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, projectEnvironmentNotFound(projectSlug, environment))
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentQualificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("environment qualification storage is unavailable"))
		return
	}
	checks := make([]state.ProjectEnvironmentQualificationCheck, 0, len(request.Checks))
	for _, check := range request.Checks {
		results := make([]state.ProjectEnvironmentQualificationResult, 0, len(check.Results))
		for _, result := range check.Results {
			results = append(results, state.ProjectEnvironmentQualificationResult{
				WorkloadSlug: result.WorkloadSlug, DeploymentID: result.DeploymentID,
				Status: result.Status, HTTPStatus: result.HTTPStatus, ErrorCode: result.ErrorCode,
			})
		}
		checks = append(checks, state.ProjectEnvironmentQualificationCheck{Name: check.Name, Status: check.Status, Results: results})
	}
	qualification, err := store.CreateProjectEnvironmentQualification(r.Context(), acct.ID, project.ID,
		environment, strings.TrimSpace(request.ReleaseSetID), request.ConfigurationVersion, request.ConfigurationHash, request.SecretRevisionHashes, checks)
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid qualification", "release_set_id, configuration identity, secret revision fingerprints, and health/smoke results must match the documented schema"))
		return
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Qualification snapshot is stale", "the active release set, environment configuration, or secret revisions changed while probes were running; rerun qualification"))
		return
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, projectEnvironmentNotFound(projectSlug, environment))
		return
	case err != nil:
		api.WriteProblem(w, api.ErrCapacity("could not record environment qualification"))
		return
	}
	writeJSON(w, http.StatusCreated, projectEnvironmentQualificationResponse(qualification))
}

func projectEnvironmentQualificationResponse(qualification state.ProjectEnvironmentQualification) api.ProjectEnvironmentQualificationResponse {
	checks := make([]api.ProjectEnvironmentQualificationCheck, 0, len(qualification.Checks))
	for _, check := range qualification.Checks {
		results := make([]api.ProjectEnvironmentQualificationResult, 0, len(check.Results))
		for _, result := range check.Results {
			results = append(results, api.ProjectEnvironmentQualificationResult{
				WorkloadSlug: result.WorkloadSlug, DeploymentID: result.DeploymentID,
				Status: result.Status, HTTPStatus: result.HTTPStatus, ErrorCode: result.ErrorCode,
			})
		}
		checks = append(checks, api.ProjectEnvironmentQualificationCheck{Name: check.Name, Status: check.Status, Results: results})
	}
	return api.ProjectEnvironmentQualificationResponse{
		ID: qualification.ID, Environment: qualification.EnvironmentSlug,
		ReleaseSetID:         qualification.ReleaseSetID,
		ConfigurationVersion: qualification.ConfigurationVersion, ConfigurationHash: qualification.ConfigurationHash,
		SecretRevisionHashes: qualification.SecretRevisionHashes,
		Status:               qualification.Status,
		Checks:               checks, CreatedAt: qualification.CreatedAt, ExpiresAt: qualification.ExpiresAt,
	}
}
