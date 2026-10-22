package apidserver

import (
	"context"
	"slices"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified drivers permanently fence the ORIGINAL deployment request namespace,
// including unknown creation outcomes and queued scheduler work, and close all
// owned runtime artifacts. Observe is read-only and cannot dispatch retirement
// or infer permanent closure from instance/process not-found. No built-in native
// adapter or public full-stage admission is enabled.
type cloneCheckpointWorkloadRetirementDriver interface {
	RetireCheckpointWorkloads(context.Context, state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) error
	ObserveCheckpointWorkloadRetirement(context.Context, state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) (state.ProjectEnvironmentCloneCheckpointWorkloadRetirementObservation, error)
}

func (s *server) retireCheckpointWorkloads(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointWorkloadRetirementDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointWorkloadRetirementStore)
	leases, leaseOK := s.store.(state.ProjectEnvironmentCloneWorkerLeaseStore)
	if !ok || !leaseOK || d == nil || s.cloneWorkerAdmission == nil {
		return l, errCloneCheckpointUnavailable
	}
	if l.Operation.Status != state.CloneOperationCompensating {
		return l, state.ErrConflict
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	var err error
	l, err = renewCloneObjectWorkerLease(ctx, leases, l, state.CloneOperationCompensating)
	if err != nil {
		return l, err
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	if _, err = store.ReserveProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l); err != nil {
		return l, err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l)
	if err != nil {
		return l, err
	}
	if in.State == "retired" {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		observed := in.Observation
		observed.Workloads = slices.Clone(observed.Workloads)
		return l, store.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, observed)
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		copied := cloneCheckpointWorkloadRetirementPlanCopy(in.Plan)
		if err = d.RetireCheckpointWorkloads(ctx, copied); err != nil {
			return l, errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	copied := cloneCheckpointWorkloadRetirementPlanCopy(in.Plan)
	observed, err := d.ObserveCheckpointWorkloadRetirement(ctx, copied)
	if err != nil {
		return l, err
	}
	observed.Workloads = slices.Clone(observed.Workloads)
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	return l, store.FinishProjectEnvironmentCloneCheckpointWorkloadRetirement(ctx, l, observed)
}

func cloneCheckpointWorkloadRetirementPlanCopy(p state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan) state.ProjectEnvironmentCloneCheckpointWorkloadRetirementPlan {
	p.Workloads = slices.Clone(p.Workloads)
	return p
}
