package state

// adr: 435. All selections and signatures are checked under the same lock.

import (
	"context"
	"errors"
	"time"
)

var _ DeploymentRuntimeProducerPresenceStore = (*MemStore)(nil)

var _ DeploymentRuntimeProducerInputStore = (*MemStore)(nil)

func (m *MemStore) GetFreshDeploymentRuntimeProducerInputs(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeProducerInputs, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return DeploymentRuntimeProducerInputs{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	return m.freshRuntimeProducerInputsLocked(ctx, accountID, appID, depID)
}

func (m *MemStore) freshRuntimeProducerInputsLocked(ctx context.Context, accountID, appID, depID string) (DeploymentRuntimeProducerInputs, error) {
	dep, err := m.artifactEvidenceOwnerLocked(accountID, appID, depID)
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	app, _, _, _ := m.registryVerificationOwnerLocked(appID, depID)
	identity, parents, err := m.runtimeProducerSetLocked(app, dep, time.Now().UTC())
	if err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	if err := ctx.Err(); err != nil {
		return DeploymentRuntimeProducerInputs{}, err
	}
	return finishRuntimeProducerInputs(identity, parents, time.Now().UTC())
}

func (m *MemStore) runtimeProducerSetLocked(app App, dep Deployment, now time.Time) (deploymentRuntimeArtifactIdentity, []runtimeProducerLease, error) {
	names, err := artifactScanWorkloads(dep.Sidecars)
	if err != nil {
		return deploymentRuntimeArtifactIdentity{}, nil, err
	}
	var identity deploymentRuntimeArtifactIdentity
	parents, bases := []runtimeProducerLease{}, map[string]bool{}
	for _, name := range names {
		selected, err := m.runtimeProducerSelectionLocked(app, dep, name, now)
		if err != nil {
			return identity, nil, err
		}
		if identity.Format == "" {
			identity = selected.Identity
		}
		identity.Artifacts = append(identity.Artifacts, selected.Artifact)
		parents = append(parents, selected.Lease)
		if id := selected.Artifact.BaseProducerID; id != "" && !bases[id] {
			base := m.baseImageProducers[id] // Selection fenced this exact current base.
			if base.PublishedAt.After(now) {
				return identity, nil, ErrApplicationStandardRuntimeStale
			}
			identity.Artifacts, bases[id] = append(identity.Artifacts, runtimeArtifactFromBaseProducer(base)), true
		}
	}
	return identity, parents, nil
}

func (m *MemStore) runtimeProducerSelectionLocked(app App, dep Deployment, name string, now time.Time) (runtimeProducerSelection, error) {
	if name == "" && m.hasSourceRootfsLocked(dep.ID) {
		return m.sourceRuntimeProducerLocked(app, dep, now)
	}
	root, err := m.runtimeArtifactRootLocked(app, dep, name)
	if err != nil {
		return runtimeProducerSelection{}, err
	}
	proof, err := m.latestRegistryVerificationLocked(app, dep, name)
	if err != nil {
		return runtimeProducerSelection{}, err
	}
	parent, err := m.artifactScanParentsLocked(runtimeProducerParentInput(root, proof), now)
	if err != nil {
		return runtimeProducerSelection{}, err
	}
	return runtimeProducerSelection{runtimeProducerIdentity(root), runtimeArtifactFromRootfs(root), registryRuntimeProducerLease(parent)}, nil
}

func (m *MemStore) HasDeploymentRuntimeProducers(ctx context.Context, accountID, appID, depID string) (bool, error) {
	if !validArtifactScanEvidenceRead(accountID, appID, depID, "") {
		return false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return false, err
	}
	_, err := m.artifactEvidenceOwnerLocked(accountID, appID, depID)
	if errors.Is(err, ErrDeploymentArtifactScanEvidenceAbsent) {
		return false, nil
	}
	return err == nil, err
}
