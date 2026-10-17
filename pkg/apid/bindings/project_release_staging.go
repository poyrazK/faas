package bindings

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func StageProjectRelease(ctx context.Context, store state.ProjectReleaseStagingStore, accountID string, project state.Project, environment string, request api.StageProjectReleaseRequest) (api.ProjectReleaseStage, *api.Problem) {
	if request.Manifest.Project != project.Slug || request.Manifest.Environment != environment || request.TTLSeconds < 1 || request.TTLSeconds > api.ProjectReleaseStageMaxTTLSeconds {
		return api.ProjectReleaseStage{}, api.ErrValidation("staging requires a matching prepared manifest and a lease of 1–1800 seconds")
	}
	out, err := store.StageProjectRelease(ctx, accountID, project.ID, request)
	return out, ReleaseStageProblem(err)
}
func ReleaseStageProblem(err error) *api.Problem {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, "not_found", "Not found", "Release stage not found.")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrValidation("staging requires a complete forward or rollback manifest with service members")
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrBindingReleasePolicyRevision), errors.Is(err, state.ErrBindingPromotionChanged):
		return api.NewProblem(http.StatusConflict, "release_staging_changed", "Release staging changed", "The manifest, routing, policy or candidate eligibility changed, or another stage is active. Prepare again or cancel the existing stage.")
	default:
		return api.ErrCapacity("release staging is unavailable")
	}
}
