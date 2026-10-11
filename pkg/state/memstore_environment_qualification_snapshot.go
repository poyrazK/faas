package state

import (
	"context"
	"time"
)

var _ EnvironmentQualificationSnapshotStore = (*MemStore)(nil)

func (m *MemStore) EnvironmentQualificationSnapshotReceipt(ctx context.Context, instanceID string) (EnvironmentQualificationSnapshotReceipt, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationSnapshotReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, exists := m.qualificationSnapshots[instanceID]
	if !exists {
		return receipt, ErrNotFound
	}
	return cloneQualificationSnapshotReceipt(receipt), nil
}

func (m *MemStore) RecordEnvironmentQualificationSnapshot(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution, proof EnvironmentQualificationSnapshot) (EnvironmentQualificationSnapshotReceipt, error) {
	var zero EnvironmentQualificationSnapshotReceipt
	if err := ValidateEnvironmentQualificationSnapshot(frame, proof); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return zero, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || claimed.ReservedInstanceID != frame.InstanceID {
		return zero, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return zero, err
	}
	execution, exists := m.qualificationExecutions[frame.InstanceID]
	ins := m.instances[frame.InstanceID]
	config, hasConfig := m.instanceRuntimeConfigReceipts[frame.InstanceID]
	if !exists || execution.Execution != frame || !execution.DispatchStarted || execution.RetiredAt != nil || ins.State != string(StateRunning) ||
		ins.WakeID != frame.WakeID || ins.NodeID != frame.NodeID || !hasConfig || config.WakeID != frame.WakeID || !m.runtimeConfigInputsFreshLocked(frame.AppID, config.Inputs) {
		return zero, ErrConflict
	}
	if prior, exists := m.qualificationSnapshots[frame.InstanceID]; exists {
		if prior.Snapshot != proof || prior.Execution != frame || !qualificationCaptureInputsEqual(prior.Inputs, config.Inputs) {
			return zero, ErrConflict
		}
		return cloneQualificationSnapshotReceipt(prior), nil
	}
	if m.qualificationSnapshots == nil {
		m.qualificationSnapshots = map[string]EnvironmentQualificationSnapshotReceipt{}
	}
	receipt := EnvironmentQualificationSnapshotReceipt{Execution: frame, Snapshot: proof, Inputs: cloneRuntimeConfigInputs(config.Inputs), RecordedAt: time.Now().UTC()}
	m.qualificationSnapshots[frame.InstanceID] = receipt
	return cloneQualificationSnapshotReceipt(receipt), nil
}
