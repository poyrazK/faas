package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) createFullProjectEnvironmentClone(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	req, problem := decodeCreateProjectEnvironmentRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.FromEnvironment == "" || req.ShareResources {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid full clone", "from_environment is required and share_resources must be false"))
		return
	}
	api.WriteProblem(w, s.fullProjectEnvironmentCloneProblem(r, acct, project))
}

func (s *server) fullProjectEnvironmentCloneProblem(r *http.Request, acct state.Account, project state.Project) *api.Problem {
	problem := fullProjectEnvironmentCloneUnavailable()
	store, ok := s.store.(state.ProjectEnvironmentCloneSchemaCoverageStore)
	if !ok {
		return problem
	}
	coverage, err := store.ProjectEnvironmentCloneSchemaCoverage(r.Context(), acct.ID, project.ID)
	if err != nil {
		return api.ErrCapacity("could not verify clone configuration schema coverage")
	}
	for _, blocker := range coverage.Blockers {
		field := "resource_coverage." + blocker.Table
		if blocker.Column != "" {
			field += "." + blocker.Column
		}
		problem.Errors = append(problem.Errors, api.FieldError{Field: field, Expected: "registered schema and complete isolated stage strategy", Got: blocker.Code})
	}
	return problem
}

// Admission stays closed until the worker can prove complete coverage and a
// coordinated data checkpoint. Never call the legacy live-copy path for full.
func fullProjectEnvironmentCloneUnavailable() *api.Problem {
	problem := api.NewProblem(http.StatusConflict, api.CodeFullEnvironmentCloneUnavailable, "Full environment clone unavailable",
		"complete resource coverage and coordinated database/object-storage capture are not yet available")
	problem.Errors = []api.FieldError{
		{Field: "resource_coverage", Expected: "every effective resource has an isolated copy strategy", Got: "incomplete"},
		{Field: "data_checkpoint", Expected: "coordinated PostgreSQL and version-pinned object-storage capture", Got: "unavailable"},
		{Field: "managed_bindings", Expected: "verified fresh target resources and credentials", Got: "unavailable"},
		{Field: "application_policies", Expected: "isolated effective policies for every rule kind", Got: "shared"},
	}
	return problem
}

func (s *server) getProjectEnvironmentCloneOperation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectEnvironmentCloneOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("clone operation status is unavailable"))
		return
	}
	if _, err := uuid.Parse(r.PathValue("clone")); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Clone operation not found", "the project has no clone operation with this ID"))
		return
	}
	op, err := store.ProjectEnvironmentCloneOperationByID(r.Context(), acct.ID, project.ID, r.PathValue("clone"))
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Clone operation not found", "the project has no clone operation with this ID"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load clone operation status"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, projectEnvironmentCloneOperationResponse(project.Slug, op))
}

func projectEnvironmentCloneOperationResponse(projectSlug string, op state.ProjectEnvironmentCloneOperation) api.ProjectEnvironmentCloneOperationResponse {
	resources := make([]api.ProjectEnvironmentCloneResourceResponse, 0, len(op.Resources))
	for _, resource := range op.Resources {
		resources = append(resources, api.ProjectEnvironmentCloneResourceResponse{
			Kind: resource.Kind, Name: resource.Name, SourceID: resource.SourceID, SourceVersion: resource.SourceVersion,
			TargetID: resource.TargetID, CapturePoint: resource.CapturePoint, Status: resource.Status,
		})
	}
	return api.ProjectEnvironmentCloneOperationResponse{
		OperationID: op.ID, ProjectSlug: projectSlug, SourceEnvironment: op.SourceEnvironment, TargetEnvironment: op.TargetEnvironment,
		SourceRevisionHash: op.SourceRevisionHash, SourceReleaseSetID: op.SourceReleaseSetID, TargetReleaseSetID: op.TargetReleaseSetID,
		Status: op.Status, Revision: op.Revision, Resources: resources, ErrorCode: op.ErrorCode,
		CreatedAt: op.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: op.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
}
