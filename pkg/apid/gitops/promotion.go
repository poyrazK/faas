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

type PromotionService struct {
	Store    state.EnvironmentGitOpsPromotionStore
	Source   func(*http.Request, state.Account) (state.EnvironmentGitSource, *api.Problem)
	Revision func(context.Context, state.Account, state.EnvironmentGitSource, string) (environmentsync.DesiredState, *api.Problem)
	Decode   func(*http.Request, any) error
}

func (s PromotionService) Preview(w http.ResponseWriter, r *http.Request, account state.Account) {
	var request api.PreviewEnvironmentGitPromotionRequest
	if s.Decode(r, &request) != nil || !api.ValidProjectEnvironmentSlug(request.FromEnvironment) || request.FromEnvironment == r.PathValue("environment") {
		api.WriteProblem(w, api.ErrValidation("Select distinct source and target environments and an immutable target commit."))
		return
	}
	to, problem := s.Source(r, account)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	originRequest := r.Clone(r.Context())
	originRequest.SetPathValue("environment", request.FromEnvironment)
	from, problem := s.Source(originRequest, account)
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
		api.WriteProblem(w, api.ErrCapacity("Promotion planning storage is unavailable."))
		return
	}
	out, err := s.Store.PreviewEnvironmentGitOpsPromotion(r.Context(), from, to, request, desired)
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_gitops_stale_promotion_plan", "Promotion observation changed", "Request and review a fresh promotion plan."))
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
