package state

import (
	"context"
	"time"
)

func (m *MemStore) completeStandardReviewArtifactSecurityLocked(ctx context.Context, snapshot *standardReviewSnapshot) error {
	normalizeStandardReviewArchivedPublishers(snapshot)
	for i := range snapshot.Applications {
		app := &snapshot.Applications[i]
		for j := range app.Artifacts {
			artifact := &app.Artifacts[j]
			inputs, err := m.freshRuntimeProducerInputsLocked(ctx, app.AccountID, app.AppID, artifact.ID)
			if standardReviewArtifactEvidenceUnavailable(err) {
				continue
			}
			if err != nil {
				return err
			}
			scan, exists := m.deploymentRuntimeScans[m.deploymentRuntimeScanCurrent[canonicalStandardUUID(artifact.ID)]]
			if !exists {
				continue
			}
			if err := validateDeploymentRuntimeScan(scan); err != nil {
				return err
			}
			inputs.CheckedAt = time.Now().UTC()
			artifact.Security, err = prepareStandardReviewArtifactSecurity(inputs, scan)
			if err != nil && !standardReviewArtifactEvidenceUnavailable(err) {
				return err
			}
		}
	}
	return nil
}
