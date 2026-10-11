package state

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentQualificationRestoreStore = (*MemStore)(nil)

func (m *MemStore) qualificationRestoreCurrentLocked(claimed EnvironmentWorkloadQualificationRequest) (EnvironmentWorkloadQualificationRequest, EnvironmentQualificationSnapshotReceipt, error) {
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return current, EnvironmentQualificationSnapshotReceipt{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ExecutionMode == api.ExecutionModeJob {
		return current, EnvironmentQualificationSnapshotReceipt{}, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return current, EnvironmentQualificationSnapshotReceipt{}, err
	}
	capture, exists := m.qualificationSnapshots[current.ReservedInstanceID]
	if !exists || !qualificationRestoreCaptureMatches(current, m.qualificationExecutions[current.ReservedInstanceID], capture) ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, capture.Inputs) {
		return current, EnvironmentQualificationSnapshotReceipt{}, ErrConflict
	}
	return current, capture, nil
}

func (m *MemStore) CreateEnvironmentQualificationRestore(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) (EnvironmentQualificationRestoreAdmission, error) {
	var zero EnvironmentQualificationRestoreAdmission
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !qualificationPlacementValid(placement) {
		return zero, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, capture, err := m.qualificationRestoreCurrentLocked(claimed)
	if err != nil {
		return zero, err
	}
	app, node := m.apps[current.AppID], m.computeNodes[placement.NodeID]
	if app.RAMMB != placement.RAMMB || !m.accounts[app.AccountID].MayDeploy() || !node.Active || node.Lifecycle != NodeLifecycleActive || placement.WakeID == capture.Execution.WakeID {
		return zero, ErrConflict
	}
	for _, status := range m.qualificationExecutions {
		if status.Execution.RequestID != current.ID || status.Execution.Attempt != current.Attempt || status.CaptureInstanceID == "" {
			continue
		}
		ins, exists := m.instances[status.Execution.InstanceID]
		if !exists || status.CaptureInstanceID != capture.Execution.InstanceID || status.RetiredAt != nil ||
			!qualificationRestoreAdmissionMatches(ins, current, placement) || status.Execution != qualificationRestoreExecution(current, ins, status.Execution.CleanupToken, capture.Execution.InstanceID) {
			return zero, ErrConflict
		}
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if !qualificationLeaseMatches(current, claimed, time.Now()) {
			return zero, ErrConflict
		}
		return EnvironmentQualificationRestoreAdmission{Instance: ins, Execution: status.Execution, Capture: cloneQualificationSnapshotReceipt(capture)}, nil
	}
	if err := m.checkNodeReservationLocked(placement.NodeID, string(StateColdBooting), placement.RAMMB); err != nil {
		return zero, err
	}
	if current.ExecutionMode == api.ExecutionModeWorker {
		if err := m.checkAccountWorkerReservationLocked(current.AppID, current.DeploymentID); err != nil {
			return zero, err
		}
	}
	ins := Instance{ID: uuid.NewString(), AppID: current.AppID, DeploymentID: current.DeploymentID, State: string(StateColdBooting), RAMMB: placement.RAMMB,
		NodeID: placement.NodeID, WakeID: placement.WakeID, StartedAt: time.Now().UTC(), Mode: qualificationInstanceMode(current), Kind: "wake"}
	if err := m.checkServiceCapacityInstanceLocked(ins); err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return zero, ErrConflict
	}
	e := qualificationRestoreExecution(current, ins, uuid.NewString(), capture.Execution.InstanceID)
	m.recordInstanceCapacityLocked(ins)
	m.instances[ins.ID] = ins
	m.qualificationExecutions[ins.ID] = EnvironmentQualificationExecutionStatus{Execution: e, CaptureInstanceID: capture.Execution.InstanceID}
	return EnvironmentQualificationRestoreAdmission{Instance: ins, Execution: e, Capture: cloneQualificationSnapshotReceipt(capture), Created: true}, nil
}

func (m *MemStore) MarkEnvironmentQualificationRestoreDispatched(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, capture, err := m.qualificationRestoreCurrentLocked(claimed)
	if err != nil {
		return err
	}
	status, exists := m.qualificationExecutions[frame.InstanceID]
	if !exists || status.Execution != frame || status.CaptureInstanceID != capture.Execution.InstanceID || status.DispatchStarted || status.RetiredAt != nil ||
		frame != qualificationRestoreExecution(current, m.instances[frame.InstanceID], frame.CleanupToken, capture.Execution.InstanceID) {
		return ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return ErrConflict
	}
	status.DispatchStarted = true
	m.qualificationExecutions[frame.InstanceID] = status
	return nil
}

func (m *MemStore) qualificationRequestUnretiredLocked(requestID string) bool {
	for _, status := range m.qualificationExecutions {
		if status.Execution.RequestID == requestID && status.RetiredAt == nil {
			return true
		}
	}
	return false
}
