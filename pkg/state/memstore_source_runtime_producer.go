package state

// adr: 435. Recheck current owner, claim, intent, signature and base under m.mu.

import "time"

func (m *MemStore) hasSourceRootfsLocked(depID string) bool {
	for _, value := range m.sourceBuildRootfs {
		if sameStandardUUID(value.Input.DeploymentID, depID) {
			return true
		}
	}
	return false
}

func (m *MemStore) sourceRuntimeProducerLocked(app App, dep Deployment, now time.Time) (runtimeProducerSelection, error) {
	root, ok := m.sourceBuildRootfs[m.sourceBuildRootfsCurrent[canonicalStandardUUID(dep.ID)]]
	origin, exists := m.buildExportPublications[root.Input.PublicationID]
	if !ok || !exists || !sourceBuildRootfsMetadataMatches(root, dep) || !sourceBuildRootfsHistoricalParent(root.Input, origin, app, dep) {
		return runtimeProducerSelection{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkSourceRuntimeIntent(root.Input, sourceBuildRootfsOwnerIntent(app, dep)); err != nil {
		return runtimeProducerSelection{}, err
	}
	claim := origin.Input.Claims
	if !sameStandardUUID(dep.BuildID, claim.BuildID) || !sameStandardUUID(m.latestSourceBuildLocked(dep.ID).ID, claim.BuildID) {
		return runtimeProducerSelection{}, ErrApplicationStandardRuntimeStale
	}
	proof := m.latestBuildExportPublicationLocked(app.AccountID, app.ID, dep.ID, claim.BuildID)
	if err := checkSourceRuntimeApproval(root, origin, proof, now); err != nil {
		return runtimeProducerSelection{}, err
	}
	if err := m.checkBuildExportPublicationLocked(proof.Input, true); err != nil {
		return runtimeProducerSelection{}, err
	}
	base, ok := m.baseImageProducers[root.Input.BaseProducerID]
	if !ok || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != base.ID || base.PublishedAt.After(now) {
		return runtimeProducerSelection{}, ErrApplicationStandardRuntimeStale
	}
	if err := checkSourceBuildRootfsBase(root.Input, base); err != nil {
		return runtimeProducerSelection{}, err
	}
	return sourceRuntimeProducerSelection(root, proof), nil
}
