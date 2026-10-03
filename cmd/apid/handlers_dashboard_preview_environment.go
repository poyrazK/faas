package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/state"
)

// dashboardPreviewEnvironment projects the same recorded PR-head members as
// the customer API and GitHub check. The caller has already loaded app through
// the account-scoped dashboard route, so no cross-account root can reach this
// read. A legacy or sibling preview has no root set and keeps its normal page.
func (s *server) dashboardPreviewEnvironment(ctx context.Context, log *slog.Logger, acct state.Account, app state.App) *dashboard.PRPreviewEnvironmentView {
	if app.PreviewOfSlug == "" || app.PreviewPrNumber <= 0 {
		return nil
	}
	reader, ok := s.store.(state.PRPreviewEnvironmentReader)
	if !ok {
		return nil
	}
	environment, err := reader.PRPreviewEnvironmentByRoot(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		log.Warn("dashboard: load PR preview environment", "app_id", app.ID, "err", err)
		return nil
	}
	result := previewset.Evaluate(environment.Set.PRNumber, app.ID, environment.Members)
	view := &dashboard.PRPreviewEnvironmentView{
		RepoFullName: environment.Set.RepoFullName,
		PRNumber:     environment.Set.PRNumber,
		CommitSHA:    environment.Set.CommitSHA,
		Phase:        result.Phase,
		Summary:      result.Summary,
		LiveCount:    result.LiveCount,
		TotalCount:   result.TotalCount,
		Members:      make([]dashboard.PRPreviewMemberView, 0, len(environment.Members)),
	}
	if environment.Set.Closed {
		view.Phase = "closed"
		view.Summary = fmt.Sprintf("Preview PR #%d is closed.", environment.Set.PRNumber)
	}
	if validProjectRepoFullName(environment.Set.RepoFullName) && environment.Set.PRNumber > 0 {
		view.PRURL = fmt.Sprintf("https://github.com/%s/pull/%d", environment.Set.RepoFullName, environment.Set.PRNumber)
	}
	for _, member := range environment.Members {
		projected, projectErr := s.previewEnvironmentMemberResponse(ctx, acct, environment.Set.PRNumber,
			environment.Set.CommitSHA, member)
		if projectErr != nil {
			log.Warn("dashboard: load PR preview member resources", "app_id", member.AppID, "err", projectErr)
			projected = api.PreviewEnvironmentMemberResponse{
				AppID: member.AppID, Slug: member.Slug, WorkloadName: member.WorkloadName,
				AppStatus: member.AppStatus, PreviewState: member.PreviewState,
				DeploymentID: member.DeploymentID, DeploymentStatus: member.DeploymentStatus,
			}
		}
		view.Members = append(view.Members, dashboard.PRPreviewMemberView{
			WorkloadName: projected.WorkloadName, Slug: projected.Slug,
			AppStatus: projected.AppStatus, PreviewState: projected.PreviewState,
			DeploymentStatus: projected.DeploymentStatus, DeploymentID: projected.DeploymentID,
			ExpiresAt: projected.ExpiresAt,
			Changes:   projected.Changes, Links: projected.Links,
		})
	}
	return view
}
