package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ EnvironmentGitOpsQualificationInstanceStore = (*MemStore)(nil)

func (m *MemStore) CreateEnvironmentWorkloadQualificationInstance(ctx context.Context, claimed EnvironmentWorkloadQualificationRequest, placement EnvironmentWorkloadQualificationPlacement) (EnvironmentWorkloadQualificationAdmission, error) {
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	if !qualificationPlacementValid(placement) {
		return EnvironmentWorkloadQualificationAdmission{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, current, err := m.qualificationLocked(claimed.ID)
	if err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) || current.ExecutionMode == api.ExecutionModeJob || current.ReservedInstanceID == "" {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	if err := m.qualificationCurrentLocked(memory, current); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	app, node := m.apps[current.AppID], m.computeNodes[placement.NodeID]
	if app.RAMMB != placement.RAMMB || !m.accounts[app.AccountID].MayDeploy() || !node.Active || node.Lifecycle != NodeLifecycleActive {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	if prior, exists := m.instances[current.ReservedInstanceID]; exists {
		if !qualificationAdmissionMatches(prior, current, placement) || !qualificationLeaseMatches(current, claimed, time.Now()) {
			return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
		}
		if err := ctx.Err(); err != nil {
			return EnvironmentWorkloadQualificationAdmission{}, err
		}
		return EnvironmentWorkloadQualificationAdmission{Instance: prior}, nil
	}
	if err := m.checkNodeReservationLocked(placement.NodeID, string(StateColdBooting), placement.RAMMB); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	if current.ExecutionMode == api.ExecutionModeWorker {
		if err := m.checkAccountWorkerReservationLocked(current.AppID, current.DeploymentID); err != nil {
			return EnvironmentWorkloadQualificationAdmission{}, err
		}
	}
	ins := Instance{ID: current.ReservedInstanceID, AppID: current.AppID, DeploymentID: current.DeploymentID, State: string(StateColdBooting),
		RAMMB: placement.RAMMB, NodeID: placement.NodeID, WakeID: placement.WakeID, StartedAt: time.Now().UTC(), Mode: qualificationInstanceMode(current), Kind: "wake"}
	if err := m.checkServiceCapacityInstanceLocked(ins); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	if !qualificationLeaseMatches(current, claimed, time.Now()) {
		return EnvironmentWorkloadQualificationAdmission{}, ErrConflict
	}
	if err := ctx.Err(); err != nil {
		return EnvironmentWorkloadQualificationAdmission{}, err
	}
	m.recordInstanceCapacityLocked(ins)
	m.instances[ins.ID] = ins
	return EnvironmentWorkloadQualificationAdmission{Instance: ins, Created: true}, nil
}

// Ordinary lifecycle writers cannot borrow a qualification reservation.
// Dedicated execution methods must first validate its capability under m.mu.
func (m *MemStore) guardInstanceRuntimeTransitionLocked(old, next Instance) error {
	if m.deployments[old.DeploymentID].EnvironmentWorkloadHeld() {
		if old.ID != next.ID || old.AppID != next.AppID || old.DeploymentID != next.DeploymentID || old.NodeID != next.NodeID || old.WakeID != next.WakeID ||
			old.Mode != next.Mode || old.RAMMB != next.RAMMB || old.Kind != next.Kind || old.JobID != next.JobID {
			return ErrConflict
		}
		if !qualificationInstanceRetired(next) && State(next.State) != StateEvictingAccountDeleting {
			return ErrConflict
		}
	} else if m.deployments[next.DeploymentID].EnvironmentWorkloadHeld() {
		return ErrConflict
	}
	return m.exclusiveRuntimeTransitionLocked(old, next)
}
