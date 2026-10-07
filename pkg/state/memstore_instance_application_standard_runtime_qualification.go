package state

import (
	"context"
	"time"
)

var _ InstanceApplicationStandardRuntimeQualificationStore = (*MemStore)(nil)

func (m *MemStore) GetInstanceApplicationStandardRuntimeQualification(ctx context.Context, orgID, appID, id string) (InstanceApplicationStandardRuntimeQualification, error) {
	if !validStandardResourceRead(orgID, appID) || !validStandardResourceRead(id, id) {
		return InstanceApplicationStandardRuntimeQualification{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return InstanceApplicationStandardRuntimeQualification{}, err
	}
	if !m.standardLogInventoryReadScopeLocked(orgID, appID) {
		return InstanceApplicationStandardRuntimeQualification{}, ErrNotFound
	}
	q, err := standardRuntimeQualification(m.standardConsumerRosterLocked(appID), id)
	if err != nil || q.Reason != "" {
		return q, err
	}
	for key, ins := range m.instances {
		if sameStandardUUID(key, id) {
			return m.standardRuntimeQualificationLocked(ctx, q, ins)
		}
	}
	return InstanceApplicationStandardRuntimeQualification{}, ErrNotFound
}

func (m *MemStore) standardRuntimeQualificationLocked(ctx context.Context, q InstanceApplicationStandardRuntimeQualification, ins Instance) (InstanceApplicationStandardRuntimeQualification, error) {
	stored, exists := m.instanceApplicationStandardAdmissions[ins.ID]
	if !exists {
		q.Reason = "native_receipt_stale"
		return q, nil
	}
	c, err := decodeInstanceStandardAdmission(ins.ID, stored.inputs, stored.CapturedAt)
	c.NodeID, c.NativeInputHash = stored.NodeID, stored.NativeInputHash
	if err != nil {
		return standardRuntimeQualificationFailure(q, "native_receipt_stale", ErrApplicationStandardRuntimeStale)
	}
	r, err := m.standardRuntimeReceiptLocked(ins.ID)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "native_receipt_stale", err)
	}
	current, err := m.standardRuntimeSnapshotLocked(ins)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "runtime_inputs_stale", err)
	}
	q.Reason = standardRuntimeQualificationInputs(q, c, r, current)
	if q.Reason != "" {
		return q, nil
	}
	evidence, err := m.freshRuntimeScanLocked(ctx, c.AccountID, c.AppID, c.DeploymentID)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "artifact_approval_stale", err)
	}
	deadline, err := standardNativeArtifactDeadline(c, evidence)
	if err != nil {
		return standardRuntimeQualificationFailure(q, "artifact_approval_stale", err)
	}
	return standardRuntimeQualificationComplete(q, deadline, time.Now()), nil
}
