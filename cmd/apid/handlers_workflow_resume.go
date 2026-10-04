package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) loadOwnedWorkflowRun(w http.ResponseWriter, r *http.Request, account state.Account) (*state.WorkflowRun, bool) {
	run, err := s.store.GetWorkflowRun(r.Context(), r.PathValue("id"))
	if err != nil {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	app, err := s.store.AppByID(r.Context(), run.AppID)
	if err != nil || app.AccountID != account.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return nil, false
	}
	return run, true
}
func (s *server) resumeWorkflowRun(w http.ResponseWriter, r *http.Request, account state.Account) {
	run, ok := s.loadOwnedWorkflowRun(w, r, account)
	if !ok {
		return
	}
	if !account.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(account.Plan))
		return
	}
	if !s.workflowRuntimeEnabled {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	var body api.ResumeWorkflowRunRequest
	if err := decodeJSONSized(r, &body, api.WorkflowResumeRequestMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowResumeRequestMaxBytes, api.WorkflowResumeRequestMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("request must contain expected_resume_count"))
		}
		return
	}
	if body.ExpectedResumeCount == nil || *body.ExpectedResumeCount < 0 || *body.ExpectedResumeCount > api.WorkflowRunMaxResumes {
		api.WriteProblem(w, api.ErrValidation("expected_resume_count must be between 0 and 16"))
		return
	}
	store, ok := s.store.(state.WorkflowResumeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow resume storage unavailable"))
		return
	}
	resumed, _, active, err := store.ResumeWorkflowRun(r.Context(), state.WorkflowResumeOptions{RunID: run.ID, AppID: run.AppID, AccountID: account.ID, ExpectedResumeCount: *body.ExpectedResumeCount})
	if err != nil {
		writeWorkflowResumeError(w, err, account.Plan, active)
		return
	}
	writeJSON(w, http.StatusOK, workflowRunResponse(resumed))
}
func writeWorkflowResumeError(w http.ResponseWriter, err error, plan api.Plan, active int) {
	var problem *api.Problem
	switch {
	case errors.Is(err, state.ErrWorkflowRunNotFound):
		problem = api.ErrWorkflowRunNotFound()
	case errors.Is(err, state.ErrWorkflowResumeConflict):
		problem = api.NewProblem(http.StatusConflict, api.CodeWorkflowResumeConflict, "Workflow resume conflict", "run must be failed or dead and expected_resume_count must match the current run")
	case errors.Is(err, state.ErrWorkflowResumeUnsafe):
		problem = api.NewProblem(http.StatusConflict, api.CodeWorkflowResumeUnsafe, "Workflow cannot be resumed", "run has no replayable failed actions, was cancelled, has active waits or calls, or has executed a failure or timeout handler")
	case errors.Is(err, state.ErrWorkflowResumeLimit):
		problem = api.NewProblem(http.StatusConflict, api.CodeWorkflowResumeLimit, "Workflow resume limit reached", "create a new run after the maximum number of resumptions").WithLimit(int64(api.WorkflowRunMaxResumes), int64(api.WorkflowRunMaxResumes))
	case errors.Is(err, state.ErrWorkflowRunQuotaExceeded):
		problem = api.ErrPlanWorkflowsQuota(plan, plan.WorkflowMaxConcurrentRuns(), active)
	case errors.Is(err, state.ErrWorkflowResumeUnavailable):
		problem = api.ErrWorkflowDeploymentUnavailable()
	default:
		problem = api.ErrCapacity("failed to resume workflow run")
	}
	api.WriteProblem(w, problem)
}
func (s *server) listWorkflowResumes(w http.ResponseWriter, r *http.Request, account state.Account) {
	run, ok := s.loadOwnedWorkflowRun(w, r, account)
	if !ok {
		return
	}
	store, ok := s.store.(state.WorkflowResumeStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow resume storage unavailable"))
		return
	}
	records, err := store.ListWorkflowResumes(r.Context(), run.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("failed to read workflow resume history"))
		return
	}
	response := api.ListWorkflowResumesResponse{Resumes: make([]api.WorkflowResumeResponse, 0, len(records))}
	for _, record := range records {
		response.Resumes = append(response.Resumes, api.WorkflowResumeResponse{RunID: run.ID, ResumeNumber: record.ResumeNumber, AccountID: record.AccountID, PreviousStatus: record.PreviousStatus, PreviousError: record.PreviousError, ResumedSteps: record.ResumedSteps, CreatedAt: record.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	writeJSON(w, http.StatusOK, response)
}
