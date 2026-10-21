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

type RollbackService struct {
	Store    state.EnvironmentGitOpsRollbackStore
	Source   func(*http.Request, state.Account) (state.EnvironmentGitSource, *api.Problem)
	Revision func(context.Context, state.Account, state.EnvironmentGitSource, string) (environmentsync.DesiredState, *api.Problem)
	Decode   func(*http.Request, any) error
}

func (s RollbackService) Preview(w http.ResponseWriter, r *http.Request, account state.Account) {
	var request api.PreviewEnvironmentGitRollbackRequest
	if s.Decode(r, &request) != nil || request.Validate() != nil {
		api.WriteProblem(w, api.ErrValidation("Select a retained serving release and an immutable target commit."))
		return
	}
	to, problem := s.Source(r, account)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	desired, problem := s.Revision(r.Context(), account, to, request.TargetCommitSHA)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if s.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("Rollback planning storage is unavailable."))
		return
	}
	out, err := s.Store.PreviewEnvironmentGitOpsRollback(r.Context(), to, request, desired)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_stale_rollback_plan", "Rollback observation changed", "Request and review a fresh rollback plan."))
		return
	}
	if errors.Is(err, state.ErrEnvironmentGitOpsRollbackReleaseUnproven) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_rollback_release_unproven", "Release is not a retained GitOps serving release", "Select a release with retained GitOps serving evidence in this environment."))
		return
	}
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "environment_gitops_rollback_release_not_found", "Retained release not found", "No retained release exists in this source and environment scope."))
		return
	}
	if err != nil {
		writePreviewError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}
