package state

// adr: 435. Called under m.mu, before recording or comparing a native capture.

func (m *MemStore) runtimeArtifactIdentityLocked(app App, dep Deployment) (*deploymentRuntimeArtifactIdentity, error) {
	retained := false
	for _, producer := range m.deploymentRegistryRootfs {
		retained = retained || sameStandardUUID(producer.Input.DeploymentID, dep.ID)
	}
	if !retained {
		return nil, nil
	}
	names, err := artifactScanWorkloads(dep.Sidecars)
	if err != nil {
		return nil, err
	}
	in := deploymentRuntimeArtifactIdentity{Format: "gregale.runtime-artifact-input.v1", AccountID: canonicalStandardUUID(app.AccountID), OrgID: registryCanonicalOrg(app.OrgID), AppID: canonicalStandardUUID(app.ID), DeploymentID: canonicalStandardUUID(dep.ID), Scope: dep.Scope}
	bases := map[string]bool{}
	for _, name := range names {
		root, err := m.runtimeArtifactRootLocked(app, dep, name)
		if err != nil {
			return nil, err
		}
		in.Artifacts = append(in.Artifacts, runtimeArtifactFromRootfs(root))
		if id := root.Input.BaseProducerID; id != "" && !bases[id] {
			base, ok := m.baseImageProducers[id]
			if !ok || m.baseImageProducerCurrent[base.Input.Artifact.StorageKey] != id || base.InputHash != root.Input.BaseInputHash || validateBaseImageProducer(base) != nil {
				return nil, ErrApplicationStandardRuntimeStale
			}
			in.Artifacts, bases[id] = append(in.Artifacts, runtimeArtifactFromBaseProducer(base)), true
		}
	}
	in, _, err = prepareRuntimeArtifactIdentity(in)
	return &in, err
}

func (m *MemStore) runtimeArtifactRootLocked(app App, dep Deployment, name string) (DeploymentRegistryRootfs, error) {
	id := m.deploymentRegistryRootfsCurrent[canonicalStandardUUID(dep.ID)+"\x00"+name]
	root, ok := m.deploymentRegistryRootfs[id]
	parent, exists := m.deploymentRegistryVerifications[root.Input.RegistryVerificationID]
	if !ok || !exists || validateRegistryRootfsStored(root) != nil || validateRegistryVerification(parent) != nil {
		return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
	}
	in := root.Input
	if !sameStandardUUID(in.AccountID, app.AccountID) || in.OrgID != registryCanonicalOrg(app.OrgID) || !sameStandardUUID(in.AppID, app.ID) || !sameStandardUUID(in.DeploymentID, dep.ID) || in.WorkloadName != name || checkRegistryVerificationOwner(parent.Input, app, dep) != nil {
		return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
	}
	layer := m.deploymentSidecarLayers[dep.ID+"\x00"+name]
	if !registryRootfsMatchesMetadata(root, dep, layer, parent) {
		return DeploymentRegistryRootfs{}, ErrApplicationStandardRuntimeStale
	}
	return root, nil
}
