package state

import (
	"context"
	"sort"
	"time"
)

var _ EnvironmentQualificationExecutionStore = (*MemStore)(nil)
var _ EnvironmentQualificationRecoveryStore = (*MemStore)(nil)

func (m *MemStore) ListEnvironmentQualificationExecutionsForRecovery(ctx context.Context, nodeID, afterInstanceID string, limit int) ([]EnvironmentQualificationExecutionStatus, error) {
	if !qualificationRecoveryPageValid(nodeID, afterInstanceID, limit) {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []EnvironmentQualificationExecutionStatus{}
	now := time.Now()
	for id, status := range m.qualificationExecutions {
		if status.Execution.NodeID != nodeID || qualificationRecoveryCursor(id) <= qualificationRecoveryCursor(afterInstanceID) || status.RetiredAt != nil {
			continue
		}
		_, request, _ := m.qualificationLocked(status.Execution.RequestID)
		if !qualificationExecutionHasActiveLease(status, request, now) {
			rows = append(rows, cloneQualificationExecutionStatus(status))
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		return qualificationRecoveryCursor(rows[i].Execution.InstanceID) < qualificationRecoveryCursor(rows[j].Execution.InstanceID)
	})
	return rows[:min(limit, len(rows))], ctx.Err()
}

func (m *MemStore) EnvironmentQualificationExecutionForRecovery(ctx context.Context, nodeID, instanceID string) (EnvironmentQualificationExecutionStatus, error) {
	if !qualificationRecoveryUUIDValid(nodeID) || !qualificationRecoveryUUIDValid(instanceID) {
		return EnvironmentQualificationExecutionStatus{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationExecutionStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, exists := m.qualificationExecutions[instanceID]
	if !exists || status.Execution.NodeID != nodeID {
		return EnvironmentQualificationExecutionStatus{}, ErrNotFound
	}
	_, request, _ := m.qualificationLocked(status.Execution.RequestID)
	if status.RetiredAt == nil && qualificationExecutionHasActiveLease(status, request, time.Now()) {
		return EnvironmentQualificationExecutionStatus{}, ErrConflict
	}
	return cloneQualificationExecutionStatus(status), ctx.Err()
}

func (m *MemStore) EnvironmentQualificationExecution(ctx context.Context, instanceID string) (EnvironmentQualificationExecutionStatus, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentQualificationExecutionStatus{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, exists := m.qualificationExecutions[instanceID]
	if !exists {
		return status, ErrNotFound
	}
	return cloneQualificationExecutionStatus(status), nil
}

func (m *MemStore) MarkEnvironmentQualificationDispatched(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, execution EnvironmentQualificationExecution) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, exists := m.qualificationExecutions[execution.InstanceID]
	if !exists || status.CaptureInstanceID != "" || !qualificationExecutionMatches(status.Execution, execution) || status.RetiredAt != nil {
		return ErrConflict
	}
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) ||
		!qualificationExecutionMatches(execution, qualificationExecution(current, m.instances[execution.InstanceID], execution.CleanupToken)) {
		return ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return err
	}
	if status.DispatchStarted {
		return ErrConflict
	} // a lost response never authorizes replay
	if err := ctx.Err(); err != nil {
		return err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return ErrConflict
	}
	status.DispatchStarted = true
	m.qualificationExecutions[execution.InstanceID] = status
	return nil
}

func (m *MemStore) RetireEnvironmentQualificationExecution(ctx context.Context, execution EnvironmentQualificationExecution, proof EnvironmentQualificationRetirement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	status, exists := m.qualificationExecutions[execution.InstanceID]
	if !exists || !qualificationExecutionMatches(status.Execution, execution) || !qualificationRetirementValid(proof, status.DispatchStarted) {
		return ErrConflict
	}
	if capture, exists := m.qualificationSnapshots[execution.InstanceID]; exists &&
		(proof.Kind != QualificationNativeRetired || proof.ReceiptID != capture.Snapshot.CaptureID ||
			proof.NativeGeneration != capture.Snapshot.NativeGeneration || proof.KernelBootID != capture.Snapshot.KernelBootID) {
		return ErrConflict
	}
	if status.CaptureInstanceID != "" && proof.Kind == QualificationNativeRetired {
		capture, hasCapture := m.qualificationSnapshots[status.CaptureInstanceID]
		original := m.qualificationExecutions[status.CaptureInstanceID]
		if !hasCapture || original.Retirement == nil || proof.NativeGeneration == capture.Snapshot.NativeGeneration ||
			proof.KernelBootID != capture.Snapshot.KernelBootID || proof.ReceiptID == original.Retirement.ReceiptID {
			return ErrConflict
		}
	}
	if status.RetiredAt != nil {
		if !qualificationRetirementEqual(status.Retirement, proof) {
			return ErrConflict
		}
		return nil
	}
	if proof.Kind == QualificationNativeRetired {
		for id, prior := range m.qualificationExecutions {
			if id != execution.InstanceID && prior.Retirement != nil && prior.Retirement.Kind == QualificationNativeRetired &&
				(prior.Retirement.ReceiptID == proof.ReceiptID || prior.Execution.NodeID == execution.NodeID && prior.Retirement.KernelBootID == proof.KernelBootID && prior.Retirement.NativeGeneration == proof.NativeGeneration) {
				return ErrConflict
			}
		}
	} else if proof.Kind == QualificationNativeEffectsAbsent {
		for id, prior := range m.qualificationExecutions {
			if id != execution.InstanceID && prior.Retirement != nil && prior.Retirement.Kind == QualificationNativeEffectsAbsent &&
				prior.Retirement.ReceiptID == proof.ReceiptID {
				return ErrConflict
			}
		}
	}
	ins, exists := m.instances[execution.InstanceID]
	if !exists || ins.AppID != execution.AppID || ins.DeploymentID != execution.DeploymentID || ins.NodeID != execution.NodeID || ins.WakeID != execution.WakeID {
		return ErrConflict
	}
	retiredState, valid := qualificationRetiredState(State(ins.State))
	if !valid {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	at := time.Now().UTC()
	status.Retirement, status.RetiredAt = &proof, &at
	m.qualificationExecutions[execution.InstanceID] = status
	ins.State, ins.TerminalAt = string(retiredState), &at
	m.instances[execution.InstanceID] = ins
	return nil
}

func (m *MemStore) qualificationExecutionUnretiredLocked(id string) bool {
	status, exists := m.qualificationExecutions[id]
	return exists && status.RetiredAt == nil
}

func (m *MemStore) guardQualificationParentDeleteLocked(appID string) error {
	for _, status := range m.qualificationExecutions {
		if status.Execution.AppID == appID && status.RetiredAt == nil {
			return ErrConflict
		}
	}
	return nil
}
