package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/state"
)

// getPreviewEnvironmentStatus is rooted in an account-owned preview app.
// Looking up the app first gives unknown and cross-account slugs the same 404
// surface; the state projection then validates every sibling against its scope.
func (s *server) getPreviewEnvironmentStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if app.PreviewOfSlug == "" || app.PreviewPrNumber <= 0 {
		previewEnvironmentNotFound(w)
		return
	}
	reader, ok := s.store.(state.PRPreviewEnvironmentReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("preview environment status is unavailable"))
		return
	}
	environment, err := reader.PRPreviewEnvironmentByRoot(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		previewEnvironmentNotFound(w)
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read preview environment status"))
		return
	}
	result := previewset.Evaluate(environment.Set.PRNumber, app.ID, environment.Members)
	response := api.PreviewEnvironmentStatusResponse{
		RootSlug: app.Slug, RepoFullName: environment.Set.RepoFullName,
		PRNumber: environment.Set.PRNumber, CommitSHA: environment.Set.CommitSHA,
		Phase: result.Phase, Ready: result.Phase == previewset.PhaseLive,
		Summary: result.Summary, LiveWorkloads: result.LiveCount, TotalWorkloads: result.TotalCount,
		Members: make([]api.PreviewEnvironmentMemberResponse, 0, len(environment.Members)),
	}
	if environment.Set.Closed {
		response.Phase = "closed"
		response.Ready = false
		response.Summary = fmt.Sprintf("Preview PR #%d is closed.", environment.Set.PRNumber)
	}
	for _, member := range environment.Members {
		response.Members = append(response.Members, api.PreviewEnvironmentMemberResponse{
			AppID: member.AppID, Slug: member.Slug, WorkloadName: member.WorkloadName,
			AppStatus: member.AppStatus, PreviewState: member.PreviewState,
			DeploymentID: member.DeploymentID, DeploymentStatus: member.DeploymentStatus,
		})
	}
	writeJSON(w, http.StatusOK, response)
}

func previewEnvironmentNotFound(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "preview_environment_not_found",
		"Preview environment not found", "the slug does not identify the root of a recorded GitHub PR preview"))
}
