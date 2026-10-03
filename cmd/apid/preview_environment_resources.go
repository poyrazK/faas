package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/previewset"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewEnvironmentMemberResponse attaches the same redacted production
// comparison and diagnostic links used by the single-preview read model. The
// candidate deployment is pinned to the recorded PR head; a newer or older
// deployment must never be represented as this environment's current artifact.
func (s *server) previewEnvironmentMemberResponse(
	ctx context.Context,
	acct state.Account,
	prNumber int,
	commitSHA string,
	member previewset.Member,
) (api.PreviewEnvironmentMemberResponse, error) {
	response := api.PreviewEnvironmentMemberResponse{
		AppID: member.AppID, Slug: member.Slug, WorkloadName: member.WorkloadName,
		AppStatus: member.AppStatus, PreviewState: member.PreviewState,
		DeploymentID: member.DeploymentID, DeploymentStatus: member.DeploymentStatus,
	}
	if member.AppID == "" || member.Slug == "" {
		return response, nil
	}
	preview, err := s.store.AppByID(ctx, member.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return response, nil
	}
	if err != nil {
		return response, fmt.Errorf("load preview workload %q: %w", member.Slug, err)
	}
	if preview.AccountID != acct.ID || preview.PreviewPrNumber != prNumber ||
		preview.Slug != member.Slug || preview.PreviewOfSlug == "" {
		return response, nil
	}
	response.ExpiresAt = preview.PreviewExpiresAt
	appResponse := s.appResponseWithContext(ctx, preview, acct.Plan)
	links := previewResourceLinks(appResponse)
	response.Links = &links

	parent, err := s.store.AppBySlug(ctx, preview.PreviewOfSlug)
	if errors.Is(err, state.ErrNotFound) || (err == nil && parent.AccountID != acct.ID) {
		return response, nil
	}
	if err != nil {
		return response, fmt.Errorf("load production parent for preview workload %q: %w", member.Slug, err)
	}
	production, err := s.previewDeployment(ctx, parent)
	if err != nil {
		return response, fmt.Errorf("load production deployment for preview workload %q: %w", member.Slug, err)
	}
	var candidate *api.DeploymentResponse
	if member.DeploymentID != "" {
		deployment, readErr := s.store.DeploymentByID(ctx, member.DeploymentID)
		switch {
		case errors.Is(readErr, state.ErrNotFound):
			// The deployment can disappear during teardown; status remains useful
			// without comparing an artifact that is no longer addressable.
		case readErr != nil:
			return response, fmt.Errorf("load current-head deployment for preview workload %q: %w", member.Slug, readErr)
		case deployment.AppID == preview.ID && deployment.Kind == state.DeploymentKindPreview && deployment.CommitSHA == commitSHA:
			projected := s.deploymentResponse(deployment, preview)
			candidate = &projected
		}
	}
	changes := previewProductionChanges(parent, preview, production, candidate)
	response.Changes = &changes
	return response, nil
}
