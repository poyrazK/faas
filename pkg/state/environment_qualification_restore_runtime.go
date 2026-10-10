package state

import (
	"context"
	"time"
)

var _ EnvironmentQualificationRestoreRuntimeStore = (*MemStore)(nil)

func qualificationRestoreRuntimeExecutionCurrent(request EnvironmentWorkloadQualificationRequest, status EnvironmentQualificationExecutionStatus, ins Instance) bool {
	return request.ReservedInstanceID != "" && status.CaptureInstanceID == request.ReservedInstanceID &&
		status.DispatchStarted && status.RetiredAt == nil && status.Execution == qualificationRestoreExecution(request, ins, status.Execution.CleanupToken, request.ReservedInstanceID)
}

func (m *MemStore) PublishEnvironmentQualificationRestoreRuntime(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, frame EnvironmentQualificationExecution, runtime EnvironmentWorkloadQualificationRuntime) (Instance, error) {
	if !qualificationRuntimeValid(runtime) {
		return Instance{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, _, err := m.qualificationRestoreCurrentLocked(claimed)
	if err != nil {
		return Instance{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || frame.CaptureInstanceID != current.ReservedInstanceID {
		return Instance{}, ErrConflict
	}
	status, exists := m.qualificationExecutions[frame.InstanceID]
	ins, instanceExists := m.instances[frame.InstanceID]
	if !exists || !instanceExists || status.Execution != frame || !qualificationRestoreRuntimeExecutionCurrent(current, status, ins) ||
		!qualificationRuntimeMatches(ins, qualificationRestoreRequestForInstance(current, ins.ID), runtime) {
		return Instance{}, ErrConflict
	}
	app, node := m.apps[current.AppID], m.computeNodes[runtime.NodeID]
	if ins.RAMMB != app.RAMMB || !m.accounts[app.AccountID].MayDeploy() || !node.Active || node.Lifecycle != NodeLifecycleActive ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, runtime.Inputs) {
		return Instance{}, ErrConflict
	}
	prior := ins
	if State(ins.State) != StateColdBooting {
		config, hasConfig := m.instanceRuntimeConfigReceipts[ins.ID]
		if !qualificationRuntimeAlreadyPublished(ins, runtime) || !hasConfig || config.WakeID != ins.WakeID || !runtimeConfigInputsEqual(config.Inputs, runtime.Inputs) {
			return Instance{}, ErrConflict
		}
	} else {
		ins.State, ins.Netns, ins.HostIP, ins.GuestUID, ins.StartedAt = string(StateRunning), runtime.Netns, runtime.HostIP, runtime.GuestUID, time.Now().UTC()
		if err := m.exclusiveRuntimeTransitionLocked(prior, ins); err != nil {
			return Instance{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return Instance{}, ErrConflict
	}
	m.instances[ins.ID] = ins
	if err := m.recordInstanceRuntimeConfigReceiptLocked(ins.ID, ins.WakeID, runtime.Inputs); err != nil {
		m.instances[ins.ID] = prior
		return Instance{}, err
	}
	return ins, nil
}

func qualificationRestoreRequestForInstance(request EnvironmentWorkloadQualificationRequest, instanceID string) EnvironmentWorkloadQualificationRequest {
	request.ReservedInstanceID = instanceID
	return request
}

func runtimeConfigInputsEqual(a, b RuntimeConfigInputs) bool {
	return qualificationCaptureInputsEqual(a, b)
}
