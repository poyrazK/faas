package state

// adr: 435

import "time"

func (m *MemStore) standardNativeArtifactDeadlineLocked(capture InstanceApplicationStandardAdmission) (time.Time, error) {
	evidence := DeploymentArtifactScanEvidence{}
	if capture.ArtifactInputHash != "" {
		dep, err := m.artifactEvidenceOwnerLocked(capture.AccountID, capture.AppID, capture.DeploymentID)
		if err != nil {
			return time.Time{}, err
		}
		evidence, err = m.artifactEvidenceComponentsLocked(capture.AccountID, capture.AppID, dep, time.Now().UTC())
		if err != nil {
			return time.Time{}, err
		}
		if err := finishArtifactScanEvidence(&evidence, time.Now().UTC()); err != nil {
			return time.Time{}, err
		}
	}
	return standardNativeArtifactDeadline(capture, evidence)
}
