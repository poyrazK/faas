package apidserver

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified drivers retire all native artifacts owned by the ORIGINAL common
// capture, including unknown capture outcomes, and permanently fence that
// capture request. They preserve source data/access and prove no native borrowers
// remain. Observe is read-only: it cannot retry retirement, repair or recapture.
// No built-in provider adapter or public full-stage admission is enabled.
type cloneCheckpointCaptureRetirementDriver interface {
	RetireCheckpointCapture(context.Context, state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) error
	ObserveCheckpointCaptureRetirement(context.Context, state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) (state.ProjectEnvironmentCloneCheckpointCaptureRetirementObservation, error)
}

func (s *server) retireCheckpointCapture(ctx context.Context, l state.ProjectEnvironmentCloneLease, d cloneCheckpointCaptureRetirementDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointCaptureRetirementStore)
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
	if _, err = store.ReserveProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l); err != nil {
		return l, err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l)
	if err != nil {
		return l, err
	}
	if in.State == "retired" {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		observed := in.Observation
		observed.Sources = append([]state.ProjectEnvironmentCloneCheckpointRetiredCapture(nil), observed.Sources...)
		return l, store.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, observed)
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		copied, err := cloneCheckpointCaptureRetirementPlanCopy(in.Plan)
		if err != nil {
			return l, err
		}
		if err = d.RetireCheckpointCapture(ctx, copied); err != nil {
			return l, errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	copied, err := cloneCheckpointCaptureRetirementPlanCopy(in.Plan)
	if err != nil {
		return l, err
	}
	observed, err := d.ObserveCheckpointCaptureRetirement(ctx, copied)
	if err != nil {
		return l, err
	}
	observed.Sources = append([]state.ProjectEnvironmentCloneCheckpointRetiredCapture(nil), observed.Sources...)
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	return l, store.FinishProjectEnvironmentCloneCheckpointCaptureRetirement(ctx, l, observed)
}

func cloneCheckpointCaptureRetirementPlanCopy(p state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan) (state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan{}, err
	}
	var out state.ProjectEnvironmentCloneCheckpointCaptureRetirementPlan
	err = json.Unmarshal(raw, &out)
	return out, err
}
