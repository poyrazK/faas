package state

import (
	"encoding/json"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) attachBuiltArtifactLocked(input Deployment, plan api.Plan) (Deployment, error) {
	frozen := candidateFrozenInputs(input)
	root, ok := m.sourceBuildRootfs[frozen.Source.BuildArtifactID]
	_, _, origin, _ := m.registryVerificationOwnerLocked(root.Input.AppID, root.Input.DeploymentID)
	if !ok {
		return input, ErrEnvironmentWorkloadPreparationUnavailable
	}
	selected, err := m.sourceRuntimeProducerLocked(m.apps[input.AppID], origin, time.Now().UTC())
	if err != nil || selected.Artifact.ProducerID != root.ID {
		return input, ErrApplicationStandardRuntimeStale
	}
	input, err = attachBuiltArtifact(input, origin, root, plan)
	if err == nil {
		err = m.requireLayerArtifactsRetainedLocked([]string{root.Input.StorageKey})
	}
	return input, err
}

func (m *MemStore) validateBuildArtifactApprovalLocked(source EnvironmentGitSource, definition api.EnvironmentDefinition) error {
	var keys []string
	for _, w := range definition.Workloads {
		if w.Source == nil || w.Source.BuildArtifactID == "" {
			continue
		}
		root, exists := m.sourceBuildRootfs[w.Source.BuildArtifactID]
		_, _, origin, _ := m.registryVerificationOwnerLocked(root.Input.AppID, root.Input.DeploymentID)
		app := m.apps[origin.AppID]
		frozen, frozenErr := origin.ScopedWorkloadRuntime()
		if frozenErr != nil || frozen == nil || frozen.SourceArchive == nil || validateSourceBuildRootfs(root) != nil || !exists || root.InputHash != w.Source.BuildArtifactHash || app.Status == AppDeleted || app.AccountID != source.AccountID || app.ProjectID != source.ProjectID || origin.DeletedAt != nil || !sourceBuildRootfsMetadataMatches(root, origin) {
			return ErrEnvironmentWorkloadPreparationUnavailable
		}
		keys = append(keys, root.Input.StorageKey)
	}
	// Validate all keys before mutating the in-memory retention inventory.
	for _, key := range keys {
		if row, exists := m.layerArtifactRetention[key]; exists && row.State != LayerArtifactRetained {
			return ErrLayerArtifactRetired
		}
	}
	return m.requireLayerArtifactsRetainedLocked(keys)
}

func (m *MemStore) buildArtifactSourcePinnedLocked(id string) bool {
	for _, memory := range m.environmentGitOps {
		if memory.source.Detached {
			continue
		}
		var definition api.EnvironmentDefinition
		if json.Unmarshal(memory.revisions[memory.source.ApprovedRevisionID].Definition, &definition) != nil {
			continue
		}
		for _, w := range definition.Workloads {
			if w.Source != nil && w.Source.BuildArtifactID == id {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) buildArtifactPinnedLocked(id string) bool {
	if m.buildArtifactSourcePinnedLocked(id) {
		return true
	}
	for _, dep := range m.deployments {
		if frozen := candidateFrozenInputs(dep); frozen.BuiltArtifact != nil && frozen.BuiltArtifact.ID == id {
			return true
		}
	}
	return false
}

func (m *MemStore) buildArtifactOriginPinnedLocked(depID string) bool {
	for id, root := range m.sourceBuildRootfs {
		if sameStandardUUID(root.Input.DeploymentID, depID) && m.buildArtifactPinnedLocked(id) {
			return true
		}
	}
	return false
}
