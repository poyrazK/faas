package state

// adr: 435, 581. Replayed historical capture is never current cache authority.

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (m *MemStore) checkStandardSnapshotRuntimeFreshLocked(snap Snapshot, source string, started time.Time) error {
	if snap.ApplicationStandardCaptureToken == "" {
		return nil
	}
	if err := m.guardStandardSnapshotPublicationLocked(snap); err != nil {
		return err
	}
	r := m.applicationStandardSnapshotCaptures[snap.ApplicationStandardCaptureToken]
	if r.Grant.Parent.Binding.InstanceID != source || r.Grant.SourceStartedAtUnixNano != started.UnixNano() {
		return ErrSnapshotRuntimeStale
	}
	current, err := m.standardRuntimeSnapshotLocked(m.instances[source])
	if err != nil {
		return ErrSnapshotRuntimeStale
	}
	matched, err := standardNativeRuntimeInputsMatch(r.inputs, current)
	if err != nil || !matched {
		return ErrSnapshotRuntimeStale
	}
	return nil
}

func checkStandardSnapshotRuntimeFresh(ctx context.Context, q sqlc.DBTX, snap Snapshot, source string, started time.Time) error {
	if snap.ApplicationStandardCaptureToken == "" {
		return nil
	}
	r, err := sqlc.New().ReadApplicationStandardSnapshotRuntimeFresh(ctx, q, sqlc.ReadApplicationStandardSnapshotRuntimeFreshParams{Token: mustPgUUID(snap.ApplicationStandardCaptureToken), DeploymentID: mustPgUUID(snap.DeploymentID)})
	if err != nil {
		return mapErr(err)
	}
	if !r.Fresh || r.SourceInstanceID != source || r.SourceStartedAtUnixNano != started.UnixNano() {
		return ErrSnapshotRuntimeStale
	}
	return nil
}
