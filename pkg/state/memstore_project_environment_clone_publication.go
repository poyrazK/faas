package state

import (
	"context"
	"strings"
	"time"
)

func (m *MemStore) ProjectEnvironmentCloneTargetOperation(_ context.Context, accountID, projectID, environment string) (ProjectEnvironmentCloneOperation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest ProjectEnvironmentCloneOperation
	for _, op := range m.projectEnvironmentCloneOperations {
		if op.AccountID == accountID && op.ProjectID == projectID && op.TargetEnvironment == environment && op.Status != CloneOperationCompensated &&
			(latest.ID == "" || op.CreatedAt.After(latest.CreatedAt) || (op.CreatedAt.Equal(latest.CreatedAt) && op.ID > latest.ID)) {
			latest = op
		}
	}
	if latest.ID == "" {
		return latest, ErrNotFound
	}
	return cloneProjectEnvironmentCloneOperation(latest), nil
}

func (m *MemStore) cloneWorkloadProofsLocked(op ProjectEnvironmentCloneOperation) ([]projectCloneWorkloadProof, error) {
	var proofs []projectCloneWorkloadProof
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		d := m.deployments[record.TargetDeploymentID]
		app := m.apps[record.AppID]
		var layers []DeploymentSidecarLayer
		for _, layer := range m.deploymentSidecarLayers {
			if layer.DeploymentID == d.ID {
				layers = append(layers, layer)
			}
		}
		signals := map[string]string{}
		for key, signal := range m.sidecarSecretReloadSignals {
			if name, ok := strings.CutPrefix(key, d.ID+"\x00"); ok {
				signals[name] = signal
			}
		}
		proof := projectCloneWorkloadProof{view: record.ProjectEnvironmentCloneWorkload,
			live: d.ID != "" && d.AppID == record.AppID && d.Scope == op.TargetEnvironment && d.Status == DeployLive &&
				app.Status != AppDeleted && app.PreviewOfSlug == "" && app.AccountID == op.AccountID && app.ProjectID == op.ProjectID}
		env, err := m.projectEnvironmentBySlugLocked(op.ProjectID, op.TargetEnvironment)
		if err != nil {
			return nil, err
		}
		proof.headHash = m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(env.ID, record.AppID)]].Hash
		proof.pinHash = m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[d.ID]].Hash
		proof.artifactHash, err = cloneWorkloadTargetArtifactHash(record, d, layers, signals)
		if err != nil {
			return nil, err
		}
		proofs = append(proofs, proof)
	}
	return proofs, nil
}

func (m *MemStore) cloneObjectProofsLocked(operationID string) []projectCloneObjectCopyProof {
	var proofs []projectCloneObjectCopyProof
	for _, manifest := range m.projectEnvironmentCloneObjectManifests {
		if manifest.OperationID != operationID {
			continue
		}
		p := projectCloneObjectCopyProof{sourceID: manifest.SourceBucketID, targetID: manifest.TargetBucketID, hash: manifest.Hash,
			capture: manifest.CapturedAt.UTC().Format(time.RFC3339Nano), objectCount: len(manifest.Objects), entryCount: len(manifest.Objects)}
		for _, item := range manifest.Objects {
			if item.CopiedAt != nil && item.TargetETag != "" && validCloneObjectSHA256(item.VerifiedSHA256) {
				p.verifiedCount++
			}
		}
		proofs = append(proofs, p)
	}
	return proofs
}

func (m *MemStore) verifyClonePublicationLocked(op ProjectEnvironmentCloneOperation, resources []ProjectEnvironmentCloneResource) error {
	if err := validateCloneObjectCopyProofs(resources, m.cloneObjectProofsLocked(op.ID)); err != nil {
		return err
	}
	proofs, err := m.cloneWorkloadProofsLocked(op)
	if err != nil {
		return err
	}
	if err := validateCloneWorkloadProofs(resources, proofs); err != nil {
		return err
	}
	var records []projectCloneWorkloadRecord
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		records = append(records, record)
	}
	return validateCloneProjectConfigProof(op, resources, records, m.projectEnvironmentConfigLatestLocked(op.ProjectID, op.TargetEnvironment))
}

func (m *MemStore) PublishProjectEnvironmentCloneReleaseSet(_ context.Context, accountID, projectID, operationID string, revision int64, ttl int) (ProjectReleaseSet, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	op, ok := m.projectEnvironmentCloneOperations[operationID]
	if !ok || op.AccountID != accountID || op.ProjectID != projectID {
		return ProjectReleaseSet{}, ErrNotFound
	}
	if op.Status == CloneOperationReady && op.TargetReleaseSetID != "" {
		if m.activeProjectReleaseSets[releaseKey(projectID, op.TargetEnvironment)] != op.TargetReleaseSetID {
			return ProjectReleaseSet{}, ErrConflict
		}
		return cloneProjectReleaseSet(m.projectReleaseSets[op.TargetReleaseSetID]), nil
	}
	if op.Status != CloneOperationPublishing || op.Revision != revision || op.TargetReleaseSetID != "" || !m.cloneOperationLeaseLiveLocked(operationID) {
		return ProjectReleaseSet{}, ErrConflict
	}
	if err := validateCloneResourceTransition(op, CloneOperationReady, op.Resources, op.ErrorCode); err != nil {
		return ProjectReleaseSet{}, err
	}
	if err := m.verifyClonePublicationLocked(op, op.Resources); err != nil {
		return ProjectReleaseSet{}, err
	}
	if m.activeProjectReleaseSets[releaseKey(projectID, op.TargetEnvironment)] != "" {
		return ProjectReleaseSet{}, ErrConflict
	}
	var members []ProjectReleaseMember
	for _, record := range m.projectEnvironmentCloneWorkloads[op.ID] {
		members = append(members, ProjectReleaseMember{AppID: record.AppID, DeploymentID: record.TargetDeploymentID})
	}
	release, err := m.publishProjectReleaseSetLocked(accountID, projectID, op.TargetEnvironment, ttl, members)
	if err != nil {
		return ProjectReleaseSet{}, err
	}
	op.Status, op.TargetReleaseSetID, op.Revision, op.UpdatedAt = CloneOperationReady, release.ID, op.Revision+1, time.Now().UTC()
	m.projectEnvironmentCloneOperations[op.ID] = op
	m.clearCloneWorkerLeaseLocked(op.ID)
	return cloneProjectReleaseSet(release), nil
}
