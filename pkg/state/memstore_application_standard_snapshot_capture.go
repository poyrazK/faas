package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

var _ ApplicationStandardSnapshotCaptureStore = (*MemStore)(nil)

func (m *MemStore) lockStandardSnapshotCaptureLocked(ctx context.Context, id, expectedState string) (Instance, runtimeadmission.Receipt, time.Time, error) {
	ins, capture, err := m.lockNativeBootInputsLocked(id, expectedState)
	if err != nil {
		return ins, runtimeadmission.Receipt{}, time.Time{}, err
	}
	boot, ok := m.instanceApplicationStandardBoots[m.instanceApplicationStandardBootTokens[id]]
	if !ok || boot.Receipt == nil || m.instanceApplicationStandardPromotionTokens[id] != "" || ins.StartedAt.IsZero() || boot.Binding.ProtocolVersion != runtimeadmission.ArtifactProtocolVersion || m.guardNativeRuntimeReceiptLocked(ins, capture) != nil {
		return ins, runtimeadmission.Receipt{}, time.Time{}, ErrApplicationStandardRuntimeStale
	}
	if validateStandardBootBinding(boot.Binding, capture, m.computeNodeRuntimeIncarnations[ins.NodeID], m.computeNodeRuntimeProtocols[ins.NodeID], time.Unix(0, boot.Receipt.CompletedAtUnixNano)) != nil {
		return ins, runtimeadmission.Receipt{}, time.Time{}, ErrApplicationStandardRuntimeStale
	}
	deadline, err := m.standardNativeArtifactDeadlineLocked(ctx, capture)
	return ins, boot.Receipt.Clone(), deadline, err
}

func (m *MemStore) IssueApplicationStandardSnapshotCapture(ctx context.Context, expectedState string, req ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error) {
	if err := req.validate(expectedState); err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	if err := ctx.Err(); err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, req.InstanceID, expectedState)
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	if ins.StartedAt.UnixNano() != req.SourceStartedAtUnixNano {
		return runtimeadmission.SnapshotGrant{}, ErrApplicationStandardRuntimeStale
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	expires, err := standardNativeGrantExpiry(now, deadline)
	if err != nil {
		return runtimeadmission.SnapshotGrant{}, err
	}
	g := req.grant(parent, now, expires)
	if g.Validate(now) != nil {
		return runtimeadmission.SnapshotGrant{}, ErrInvalidArgument
	}
	if old, ok := m.applicationStandardSnapshotCaptures[g.Token]; ok {
		return standardSnapshotIssueRetry(old, expectedState, g, now, deadline)
	}
	for _, snap := range m.snapshots {
		if snap.StorageKey == g.MemoryKey {
			return runtimeadmission.SnapshotGrant{}, ErrConflict
		}
	}
	if m.applicationStandardSnapshotCaptures == nil {
		m.applicationStandardSnapshotCaptures = map[string]ApplicationStandardSnapshotCaptureRecord{}
	}
	m.applicationStandardSnapshotCaptures[g.Token] = ApplicationStandardSnapshotCaptureRecord{ExpectedState: expectedState, Grant: g.Clone(), CreatedAt: now, inputs: append([]byte(nil), m.instanceApplicationStandardAdmissions[ins.ID].inputs...)}
	return g.Clone(), nil
}

func (m *MemStore) guardStandardSnapshotPublicationLocked(snap Snapshot) error {
	if snap.ApplicationStandardCaptureToken == "" {
		for _, r := range m.applicationStandardSnapshotCaptures {
			if r.Grant.MemoryKey == snap.StorageKey {
				return ErrInvalidArgument
			}
		}
		return nil // Legacy cache data is not a measured capture receipt.
	}
	r, ok := m.applicationStandardSnapshotCaptures[snap.ApplicationStandardCaptureToken]
	dep, found := m.deployments[snap.DeploymentID]
	app, owned := m.apps[dep.AppID]
	if !ok || !found || !owned || standardSnapshotRecordValid(r) != nil || r.Acknowledgment == nil {
		return ErrInvalidArgument
	}
	g, a := r.Grant, r.Acknowledgment
	b := g.Parent.Binding
	tier := SnapshotTierInit
	if g.Mode == "warm" {
		tier = SnapshotTierWarm
	}
	if !sameStandardUUID(dep.ID, b.DeploymentID) || !sameStandardUUID(app.ID, b.AppID) || !sameStandardUUID(app.AccountID, b.AccountID) ||
		g.Mode != "warm" && g.Mode != "park" || snap.StorageKey != g.MemoryKey || snap.FCVersion != g.FCVersion || snap.Tier != tier ||
		SnapshotVMStateKey(snap) != g.VMStateKey || SnapshotDriveKey(snap) != g.PrivateDriveKey ||
		snap.MemBytes != a.Capture.Memory.Bytes || snap.DiskBytes != a.Capture.VMState.Bytes || snap.StoredBytes < 0 {
		return ErrInvalidArgument
	}
	return nil
}

func (m *MemStore) PublishApplicationStandardSnapshotCapture(ctx context.Context, ack runtimeadmission.SnapshotAcknowledgment) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.applicationStandardSnapshotCaptures[ack.Grant.Token]
	if !ok || !r.Grant.Equal(ack.Grant) {
		return ErrApplicationStandardRuntimeStale
	}
	if r.Acknowledgment != nil {
		if !r.Acknowledgment.Equal(ack) {
			return ErrConflict
		}
		return nil // A lost response can be read after source residency is gone.
	}
	ins, parent, deadline, err := m.lockStandardSnapshotCaptureLocked(ctx, r.Grant.Parent.Binding.InstanceID, r.ExpectedState)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if !parent.Equal(r.Grant.Parent) || ins.StartedAt.UnixNano() != r.Grant.SourceStartedAtUnixNano || !standardNativeGrantWithinArtifactLease(r.Grant.ExpiresAtUnixNano, deadline) || ack.Check(r.Grant, now) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	owned := ack.Clone()
	r.Acknowledgment, r.ReceivedAt = &owned, now
	m.applicationStandardSnapshotCaptures[r.Grant.Token] = r
	return nil
}

func (m *MemStore) GetApplicationStandardSnapshotCapture(ctx context.Context, accountID, appID, deploymentID, token string) (ApplicationStandardSnapshotCaptureRecord, error) {
	if err := ctx.Err(); err != nil {
		return ApplicationStandardSnapshotCaptureRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.applicationStandardSnapshotCaptures[token]
	b := r.Grant.Parent.Binding
	if !ok || b.AccountID != accountID || b.AppID != appID || b.DeploymentID != deploymentID {
		return ApplicationStandardSnapshotCaptureRecord{}, ErrNotFound
	}
	return r.Clone(), standardSnapshotRecordValid(r)
}

func (m *MemStore) deleteAppStandardSnapshotCapturesLocked(id string) {
	for token, r := range m.applicationStandardSnapshotCaptures {
		if sameStandardUUID(r.Grant.Parent.Binding.AppID, id) {
			delete(m.applicationStandardSnapshotCaptures, token)
		}
	}
}
