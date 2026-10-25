package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/promotion"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsPromotionStore = (*MemStore)(nil)

func (m *MemStore) PreviewEnvironmentGitOpsPromotion(ctx context.Context, from, to EnvironmentGitSource, request api.PreviewEnvironmentGitPromotionRequest, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitPromotionResponse, error) {
	var zero api.PreviewEnvironmentGitPromotionResponse
	if err := validateGitPromotion(from, to, request, desired); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	origin, target := m.environmentGitOps[from.ID], m.environmentGitOps[to.ID]
	if origin == nil || target == nil || origin.source.AccountID != from.AccountID || target.source.AccountID != to.AccountID {
		return zero, ErrNotFound
	}
	if !promotionSourceMatches(origin.source, from) || !promotionSourceMatches(target.source, to) {
		return zero, ErrConflict
	}
	revision, ok := origin.revisions[origin.source.ApprovedRevisionID]
	if !ok {
		return zero, ErrConflict
	}
	input := promotionInput(origin.source, target.source, cloneEnvironmentRevision(revision), request, desired)
	var err error
	input.Evidence, err = m.currentEnvironmentGitOpsWorkloadEvidenceLocked(from.AccountID, from.ID)
	if err != nil {
		return zero, err
	}
	release := m.projectReleaseSets[m.activeProjectReleaseSets[releaseKey(from.ProjectID, from.EnvironmentSlug)]]
	if release.Active && release.AccountID == from.AccountID && release.ProjectID == from.ProjectID && release.EnvironmentSlug == from.EnvironmentSlug {
		input.ReleaseID = release.ID
		if input.Evidence != nil {
			for _, workload := range input.Evidence.Workloads {
				dep, exists := m.deployments[workload.DeploymentID]
				if !exists || releaseMemberForApp(release, workload.AppID) != dep.ID || dep.AppID != workload.AppID || dep.Status != DeployLive || dep.EnvironmentWorkloadHeld() || normalizedDeploymentScope(dep.Scope) != from.EnvironmentSlug {
					continue
				}
				artifact := api.EnvironmentGitPromotionArtifact{Resource: workload.Resource, DeploymentID: dep.ID, BuildID: dep.BuildID, Kind: string(dep.Kind), ImageDigest: dep.ImageDigest}
				if dep.Kind != DeploymentKindImage {
					selected, err := m.sourceRuntimeProducerLocked(m.apps[dep.AppID], dep, input.Now)
					if err == nil {
						root := m.sourceBuildRootfs[selected.Artifact.ProducerID]
						artifact.BuildArtifactID, artifact.BuildArtifactHash, artifact.ArtifactDigest = root.ID, root.InputHash, root.Input.ArtifactDigest
						_, _, origin, _ := m.registryVerificationOwnerLocked(root.Input.AppID, root.Input.DeploymentID)
						artifact.BuildID, artifact.BuildCommitSHA = origin.BuildID, origin.CommitSHA
					}
				}
				input.Artifacts = append(input.Artifacts, artifact)
			}
		}
	}
	out, proposed, err := promotion.Prepare(input)
	if err != nil {
		return zero, ErrInvalidArgument
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(target), proposed)
	if err != nil {
		return zero, err
	}
	return finishGitPromotion(input, out, proposed, observed, request.ExpectedPlanHash)
}
