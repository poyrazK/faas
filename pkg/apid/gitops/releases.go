package gitops

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type RecoveryService struct {
	Store    state.EnvironmentGitOpsRecoveryStore
	Source   func(*http.Request, state.Account) (state.EnvironmentGitSource, *api.Problem)
	Revision func(context.Context, state.Account, state.EnvironmentGitSource, string) (environmentsync.DesiredState, *api.Problem)
}

func (s RecoveryService) List(w http.ResponseWriter, r *http.Request, account state.Account) {
	out, problem := s.Observe(r, account)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(out)
}

func (s RecoveryService) Observe(r *http.Request, account state.Account) (api.ListEnvironmentGitReleasesResponse, *api.Problem) {
	var zero api.ListEnvironmentGitReleasesResponse
	request := api.ListEnvironmentGitReleasesRequest{TargetCommitSHA: r.URL.Query().Get("target_commit_sha"), Before: r.URL.Query().Get("before")}
	if request.Validate() != nil {
		return zero, api.ErrValidation("Select an immutable target commit and a canonical release cursor.")
	}
	source, problem := s.Source(r, account)
	if problem != nil {
		return zero, problem
	}
	desired, problem := s.Revision(r.Context(), account, source, request.TargetCommitSHA)
	if problem != nil {
		return zero, problem
	}
	if s.Store == nil {
		return zero, api.ErrCapacity("Recovery listing storage is unavailable.")
	}
	out, err := s.Store.ListEnvironmentGitReleases(r.Context(), source, request, desired)
	if err != nil {
		return zero, previewProblem(err)
	}
	return out, nil
}
