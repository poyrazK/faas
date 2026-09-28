package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) registerScenarioTest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid run ID", "run_id must be 32 lowercase hexadecimal characters"))
		return
	}
	var req api.RegisterScenarioTestRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if len(req.Members) == 0 || len(req.Members) > 16 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid workloads", "members must contain 1 to 16 workloads"))
		return
	}
	members := make([]state.ScenarioTestMember, 0, len(req.Members))
	for _, spec := range req.Members {
		if !validSlug(spec.Workload) || !validSlug(spec.AppSlug) {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid workload", "workload and app_slug must be valid slugs"))
			return
		}
		app, err := s.store.AppBySlug(r.Context(), spec.AppSlug)
		if err != nil || app.AccountID != acct.ID || app.PreviewPrNumber != 0 || app.PreviewOfSlug == "" ||
			devSessionSlug(acct.ID, app.PreviewOfSlug, runID) != app.Slug {
			s.notFound(w, "no such test developer session")
			return
		}
		members = append(members, state.ScenarioTestMember{Workload: spec.Workload, AppID: app.ID})
	}
	if err := s.store.RegisterScenarioTestMembers(r.Context(), acct.ID, runID, members); err != nil {
		switch {
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Test environment conflict", "run or workload is already registered"))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "test developer session is unavailable")
		default:
			api.WriteProblem(w, api.ErrCapacity("register test environment"))
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteScenarioTest(w http.ResponseWriter, r *http.Request, acct state.Account) {
	runID := r.PathValue("run_id")
	if !validDevWorkspaceID(runID) || runID == "" {
		s.notFound(w, "no such test environment")
		return
	}
	if err := s.store.DeleteScenarioTestMembers(r.Context(), acct.ID, runID); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Test environment is active", "destroy its developer sessions before deleting the environment"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("delete test environment"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
