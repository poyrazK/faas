// Package gitops provides customer-facing read-only GitOps assessments.
package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type PreviewService struct {
	Store    state.EnvironmentGitOpsPreviewStore
	Source   func(*http.Request, state.Account) (state.EnvironmentGitSource, *api.Problem)
	Revision func(context.Context, state.Account, state.EnvironmentGitSource, string) (environmentsync.DesiredState, *api.Problem)
	Decode   func(*http.Request, any) error
}

func (s PreviewService) Preview(w http.ResponseWriter, r *http.Request, account state.Account) {
	source, problem := s.Source(r, account)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var request api.PreviewEnvironmentGitRevisionRequest
	if s.Decode(r, &request) != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid candidate plan request."))
		return
	}
	desired, problem := s.Revision(r.Context(), account, source, request.CommitSHA)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if s.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("Candidate planning storage is unavailable."))
		return
	}
	preview, err := s.Store.PreviewEnvironmentGitOpsCommit(r.Context(), source, request.CommitSHA, desired)
	if err != nil {
		writePreviewError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(preview)
}

func writePreviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_stale_plan", "Source changed", "Request a fresh plan for the current binding."))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "environment_git_source_not_found", "Git source not found", "No active source exists in this environment."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("Invalid candidate plan."))
	default:
		api.WriteProblem(w, api.ErrCapacity("Could not observe the environment."))
	}
}
