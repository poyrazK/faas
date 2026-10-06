package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentQualificationServiceStore = (*MemStore)(nil)

func (m *MemStore) qualificationNetworkInstanceLocked(nodeID, hostIP string) (Instance, error) {
	var caller Instance
	for _, ins := range m.instances {
		if ins.NodeID != nodeID || ins.HostIP != hostIP || (ins.State != string(StateRunning) && ins.State != string(StateDraining)) {
			continue
		}
		if caller.ID != "" {
			return Instance{}, ErrConflict // An ambiguous network slot grants no identity.
		}
		caller = ins
	}
	return caller, nil
}

func (m *MemStore) qualificationServiceRuntimeInstanceLocked(request EnvironmentWorkloadQualificationRequest) (Instance, EnvironmentQualificationExecutionStatus, error) {
	var restore Instance
	var restoreStatus EnvironmentQualificationExecutionStatus
	for id, status := range m.qualificationExecutions {
		if status.Execution.RequestID != request.ID || status.Execution.Attempt != request.Attempt || status.CaptureInstanceID != request.ReservedInstanceID {
			continue
		}
		if restore.ID != "" {
			return Instance{}, EnvironmentQualificationExecutionStatus{}, ErrConflict
		}
		restore, restoreStatus = m.instances[id], status
	}
	if restore.ID != "" {
		if !qualificationServiceExecutionCurrent(request, restoreStatus, restore, time.Now()) {
			return Instance{}, EnvironmentQualificationExecutionStatus{}, ErrConflict
		}
		return restore, restoreStatus, nil
	}
	instance := m.instances[request.ReservedInstanceID]
	status := m.qualificationExecutions[request.ReservedInstanceID]
	if !qualificationServiceExecutionCurrent(request, status, instance, time.Now()) {
		return Instance{}, EnvironmentQualificationExecutionStatus{}, ErrConflict
	}
	return instance, status, nil
}

func (m *MemStore) EnvironmentQualificationNetworkCaller(ctx context.Context, nodeID, hostIP string) (bool, error) {
	if !qualificationNetworkIdentityValid(nodeID, hostIP) {
		return false, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, err := m.qualificationNetworkInstanceLocked(nodeID, hostIP)
	return m.deployments[ins.DeploymentID].EnvironmentWorkloadHeld(), err
}

func (m *MemStore) ResolveEnvironmentQualificationService(ctx context.Context, request EnvironmentQualificationServiceRequest) (EnvironmentQualificationServiceRoute, error) {
	var zero EnvironmentQualificationServiceRoute
	if !qualificationServiceRequestValid(request) {
		return zero, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	ins, err := m.qualificationNetworkInstanceLocked(request.NodeID, request.HostIP)
	if err != nil || ins.ID == "" || !m.deployments[ins.DeploymentID].EnvironmentWorkloadHeld() {
		return zero, ErrConflict
	}
	callerStatus := m.qualificationExecutions[ins.ID]
	memory, caller, err := m.qualificationLocked(callerStatus.Execution.RequestID)
	if err != nil || caller.GraphID != request.GraphID || m.qualificationCurrentLocked(memory, caller) != nil ||
		!qualificationServiceExecutionCurrent(caller, callerStatus, ins, time.Now()) {
		return zero, ErrConflict
	}
	binding, exists := caller.FrozenInputs.ServiceBindings[request.Binding]
	if !exists {
		return zero, ErrConflict
	}
	var target EnvironmentWorkloadQualificationRequest
	for _, candidate := range memory.qualifications {
		if candidate.GraphID == caller.GraphID && candidate.Resource == "workload/"+binding.Workload && candidate.AppID == binding.TargetAppID {
			target = candidate
		}
	}
	targetIns, targetStatus, err := m.qualificationServiceRuntimeInstanceLocked(target)
	if err != nil {
		return zero, ErrConflict
	}
	if protocol := m.apps[target.AppID].AppProtocol; protocol != "" && protocol != api.AppProtocolHTTP1 {
		return zero, ErrEnvironmentWorkloadPreparationUnavailable
	}
	for i, endpoint := range []Instance{ins, targetIns} {
		scope := caller.FrozenInputs.Scope
		if i == 1 {
			scope = target.FrozenInputs.Scope
		}
		node := m.computeNodes[endpoint.NodeID]
		config, hasConfig := m.instanceRuntimeConfigReceipts[endpoint.ID]
		if !node.Active || node.Lifecycle != NodeLifecycleActive || !hasConfig || config.WakeID != endpoint.WakeID || config.Inputs.Scope != scope ||
			!m.runtimeConfigInputsFreshLocked(endpoint.AppID, config.Inputs) {
			return zero, ErrConflict
		}
	}
	return qualificationServiceRoute(caller, target, callerStatus, targetStatus, request.Binding)
}
