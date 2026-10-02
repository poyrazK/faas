package state

import (
	"context"
	"time"
)

var _ EnvironmentGitOpsQualificationRuntimeStore = (*MemStore)(nil)

func (m *MemStore) PublishEnvironmentWorkloadQualificationRuntime(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, runtime EnvironmentWorkloadQualificationRuntime) (Instance, error) {
	if !qualificationRuntimeValid(runtime) {
		return Instance{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return Instance{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return Instance{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ReservedInstanceID == "" {
		return Instance{}, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return Instance{}, err
	}
	ins, exists := m.instances[current.ReservedInstanceID]
	node := m.computeNodes[runtime.NodeID]
	if !exists || !qualificationRuntimeMatches(ins, current, runtime) || ins.RAMMB != m.apps[current.AppID].RAMMB || !node.Active || node.Lifecycle != NodeLifecycleActive ||
		!m.runtimeConfigInputsFreshLocked(current.AppID, runtime.Inputs) {
		return Instance{}, ErrConflict
	}
	prior := ins
	if State(ins.State) != StateColdBooting && !qualificationRuntimeAlreadyPublished(ins, runtime) {
		return Instance{}, ErrConflict
	}
	if State(ins.State) == StateColdBooting {
		ins.State, ins.Netns, ins.HostIP, ins.GuestUID, ins.StartedAt = string(StateRunning), runtime.Netns, runtime.HostIP, runtime.GuestUID, time.Now().UTC()
		// Authority was validated under the same mutex. Ordinary writers still
		// use guardInstanceRuntimeTransitionLocked and cannot enter this path.
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
