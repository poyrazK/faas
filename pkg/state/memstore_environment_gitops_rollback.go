package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentgitops/rollback"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsRollbackStore = (*MemStore)(nil)

func (m *MemStore) PreviewEnvironmentGitOpsRollback(ctx context.Context, source EnvironmentGitSource, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState) (api.PreviewEnvironmentGitRollbackResponse, error) {
	var zero api.PreviewEnvironmentGitRollbackResponse
	if err := validateGitRollback(source, request, desired); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory := m.environmentGitOps[source.ID]
	if memory == nil || memory.source.AccountID != source.AccountID {
		return zero, ErrNotFound
	}
	if !promotionSourceMatches(memory.source, source) {
		return zero, ErrConflict
	}
	return m.previewEnvironmentGitOpsRollbackLocked(memory, request, desired, time.Now().UTC())
}

func (m *MemStore) previewEnvironmentGitOpsRollbackLocked(memory *environmentGitOpsMemory, request api.PreviewEnvironmentGitRollbackRequest, desired environmentsync.DesiredState, now time.Time) (api.PreviewEnvironmentGitRollbackResponse, error) {
	var zero api.PreviewEnvironmentGitRollbackResponse
	source := memory.source
	release, exists := m.projectReleaseSets[request.ReleaseSetID]
	if !exists || release.AccountID != source.AccountID || release.ProjectID != source.ProjectID || release.EnvironmentSlug != source.EnvironmentSlug {
		return zero, ErrNotFound
	}
	var graph EnvironmentWorkloadGraph
	var receipt EnvironmentWorkloadServingReceipt
	for _, g := range memory.graphs {
		r, exists := m.environmentWorkloadServingReceipts[g.ID]
		if exists && r.ReleaseSetID == release.ID {
			if graph.ID != "" {
				return zero, ErrEnvironmentGitOpsRollbackReleaseUnproven
			}
			graph, receipt = g, cloneEnvironmentWorkloadServingReceipt(r)
		}
	}
	receipt.ReleaseCreatedAt = release.CreatedAt
	revision := memory.revisions[graph.RevisionID]
	input, err := retainedRollbackInput(source, request, desired, release, graph, revision, receipt)
	if err != nil {
		return zero, err
	}
	input.Now = now
	input.CurrentReleaseSetID = m.activeProjectReleaseSets[releaseKey(source.ProjectID, source.EnvironmentSlug)]
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return zero, err
	}
	for _, member := range graph.Members {
		dep := m.deployments[releaseMemberForApp(release, member.AppID)]
		if !rollbackMemberCompatible(&input, member, dep, observed) {
			continue
		}
		artifact := api.EnvironmentGitPromotionArtifact{Resource: member.Resource, DeploymentID: dep.ID, BuildID: dep.BuildID, Kind: string(dep.Kind), ImageDigest: dep.ImageDigest}
		if dep.Kind != DeploymentKindImage {
			selected, err := m.sourceRuntimeProducerLocked(m.apps[dep.AppID], dep, input.Now)
			if err == nil {
				root := m.sourceBuildRootfs[selected.Artifact.ProducerID]
				if graph.PreparedAt == nil || root.PublishedAt.After(*graph.PreparedAt) {
					input.BlockingReasons = append(input.BlockingReasons, member.Resource+": rollback_artifact_replaced")
					continue
				}
				_, _, origin, _ := m.registryVerificationOwnerLocked(root.Input.AppID, root.Input.DeploymentID)
				artifact.BuildArtifactID, artifact.BuildArtifactHash, artifact.ArtifactDigest = root.ID, root.InputHash, root.Input.ArtifactDigest
				artifact.BuildID, artifact.BuildCommitSHA = origin.BuildID, origin.CommitSHA
			}
		}
		input.Artifacts = append(input.Artifacts, artifact)
	}
	// Observe the proposed definition as well, so source compatibility checks and
	// ownership are evaluated against the actual exported changes.
	_, proposed, err := rollback.Prepare(input)
	if err != nil {
		return zero, err
	}
	observed, err = compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), proposed)
	if err != nil {
		return zero, err
	}
	return finishGitRollback(input, observed, request.ExpectedPlanHash)
}
