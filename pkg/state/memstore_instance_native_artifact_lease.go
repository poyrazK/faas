package state

// adr: 435

import (
	"context"
	"time"
)

func (m *MemStore) standardNativeArtifactDeadlineLocked(capture InstanceApplicationStandardAdmission) (time.Time, error) {
	evidence := DeploymentRuntimeScanEvidence{}
	if capture.ArtifactInputHash != "" {
		var err error
		evidence, err = m.freshRuntimeScanLocked(context.Background(), capture.AccountID, capture.AppID, capture.DeploymentID)
		if err != nil {
			return time.Time{}, err
		}
	}
	return standardNativeArtifactDeadline(capture, evidence)
}
