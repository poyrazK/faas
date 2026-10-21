package state

import (
	"context"
	"errors"
)

func (m *MemStore) qualifyStandardArtifactsLocked(ctx context.Context, r ApplicationStandardConsumerRoster, q standardApplicationQualification) (standardApplicationQualification, error) {
	count := 0
	for _, dep := range m.deployments {
		if !sameStandardUUID(dep.AppID, r.AppID) || !m.standardRetainedArtifactLocked(dep) {
			continue
		}
		count++
		raw, err := m.standardRuntimeSnapshotLocked(Instance{AppID: dep.AppID, DeploymentID: dep.ID})
		if err != nil {
			return standardApplicationQualification{reason: "artifact_approval_stale"}, standardObservationArtifactError(err)
		}
		evidence, err := m.freshRuntimeScanLocked(ctx, r.AccountID, r.AppID, dep.ID)
		if err != nil {
			return standardApplicationQualification{reason: "artifact_approval_stale"}, standardObservationArtifactError(err)
		}
		artifact := standardObservationArtifact(raw, evidence)
		if artifact.reason != "" {
			return artifact, nil
		}
		q.restrict(artifact.until)
		for _, snapshot := range m.snapshots {
			if snapshot.DeploymentID != dep.ID || snapshot.Stale || snapshot.DeletePending {
				continue
			}
			record := m.applicationStandardSnapshotCaptures[snapshot.ApplicationStandardCaptureToken].Clone()
			if err := m.guardStandardSnapshotPublicationLocked(snapshot); errors.Is(err, ErrInvalidArgument) {
				return standardApplicationQualification{reason: "snapshot_inputs_stale"}, nil
			} else if err != nil {
				return q, err
			}
			if reason := standardObservationSnapshot(record, raw, r); reason != "" {
				return standardApplicationQualification{reason: reason}, nil
			}
			for _, node := range m.computeNodes {
				if sameStandardUUID(node.ID, record.Grant.Parent.Binding.NodeID) {
					q.restrict(node.LastHeartbeatAt.Add(DefaultHeartbeatStaleness))
				}
			}
		}
	}
	if count == 0 {
		q.reason = "artifact_inventory_empty"
	}
	return q, nil
}

func (m *MemStore) standardRetainedArtifactLocked(dep Deployment) bool {
	live, snapshot := false, false
	for _, ins := range m.instances {
		live = live || sameStandardUUID(ins.DeploymentID, dep.ID) && IsLive(ins.State)
	}
	for _, row := range m.snapshots {
		snapshot = snapshot || sameStandardUUID(row.DeploymentID, dep.ID) && !row.Stale && !row.DeletePending
	}
	return standardRetainedArtifact(string(dep.Status), live, snapshot)
}

func standardObservationArtifactError(err error) error {
	if standardReviewArtifactEvidenceUnavailable(err) {
		return nil
	}
	return err
}
