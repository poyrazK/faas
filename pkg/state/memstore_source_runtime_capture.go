package state

import "github.com/onebox-faas/faas/pkg/api"

// adr: 435. Historical producer capture does not renew signature or scan authority.

func (m *MemStore) sourceRuntimeCaptureRootLocked(app App, dep Deployment) (SourceBuildRootfs, error) {
	if frozen := candidateFrozenInputs(dep); frozen.BuiltArtifact != nil {
		if !builtArtifactMatchesDeployment(dep, frozen) {
			return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
		}
		root, exists := m.sourceBuildRootfs[frozen.BuiltArtifact.ID]
		_, _, origin, _ := m.registryVerificationOwnerLocked(dep.AppID, frozen.BuiltArtifact.DeploymentID)
		if !exists || candidateFrozenInputs(origin).BuiltArtifact != nil {
			return root, ErrApplicationStandardRuntimeStale
		}
		if _, err := attachBuiltArtifact(dep, origin, root, api.PlanScale); err != nil {
			return root, err
		}
		current, err := m.sourceRuntimeCaptureRootLocked(app, origin)
		if err == nil && current.ID != root.ID {
			err = ErrApplicationStandardRuntimeStale
		}
		return current, err
	}
	root, ok := m.sourceBuildRootfs[m.sourceBuildRootfsCurrent[canonicalStandardUUID(dep.ID)]]
	origin, exists := m.buildExportPublications[root.Input.PublicationID]
	if !ok || !exists || validateSourceBuildRootfs(root) != nil || !sourceBuildRootfsMetadataMatches(root, dep) ||
		!sourceBuildRootfsHistoricalParent(root.Input, origin, app, dep) ||
		root.PublishedAt.Before(origin.VerifiedAt) || root.ExpiresAt.After(origin.ExpiresAt) ||
		!sameStandardUUID(dep.BuildID, origin.Input.Claims.BuildID) ||
		!sameStandardUUID(m.latestSourceBuildLocked(dep.ID).ID, origin.Input.Claims.BuildID) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	kind, runtime := SourceBuildRootfsKindForClaim(app, dep, origin.Input.Claims.Runtime)
	if root.Input.Kind != kind || root.Input.Runtime != runtime {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	base, ok := m.baseImageProducers[root.Input.BaseProducerID]
	if !ok || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkSourceBuildRootfsBase(root.Input, base); err != nil {
		return SourceBuildRootfs{}, err
	}
	return root, nil
}
