package state

// adr: 435. Historical producer capture does not renew signature or scan authority.

func (m *MemStore) sourceRuntimeCaptureRootLocked(app App, dep Deployment) (SourceBuildRootfs, error) {
	root, ok := m.sourceBuildRootfs[m.sourceBuildRootfsCurrent[canonicalStandardUUID(dep.ID)]]
	origin, exists := m.buildExportPublications[root.Input.PublicationID]
	if !ok || !exists || validateSourceBuildRootfs(root) != nil || !sourceBuildRootfsMetadataMatches(root, dep) ||
		!sourceBuildRootfsHistoricalParent(root.Input, origin, app, dep) ||
		root.PublishedAt.Before(origin.VerifiedAt) || root.ExpiresAt.After(origin.ExpiresAt) ||
		!sameStandardUUID(dep.BuildID, origin.Input.Claims.BuildID) ||
		!sameStandardUUID(m.latestSourceBuildLocked(dep.ID).ID, origin.Input.Claims.BuildID) {
		return SourceBuildRootfs{}, ErrApplicationStandardRuntimeStale
	}
	kind, runtime := SourceBuildRootfsKind(app, dep)
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
