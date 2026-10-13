package apidserver

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/state"
)

// A qualified driver permanently fences the ORIGINAL common capture and any
// original release request, including queued requests, and restores the exact
// owned native sources without changing their data. Observe independently reads
// that closure; it never retries compensation or invents proof from absence.
// No built-in provider adapter or public full-stage admission is enabled.
type cloneCheckpointSourceCompensationDriver interface {
	CompensateCheckpointSources(context.Context, state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) error
	ObserveCheckpointSourceCompensation(context.Context, state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensationObservation, error)
}

// Initial reservation receives ORIGINAL capture evidence. Subsequent recovery
// reads the immutable journal without rebuilding a plan from live sources.
func (s *server) compensateCheckpointSources(ctx context.Context, l state.ProjectEnvironmentCloneLease, p *state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan, d cloneCheckpointSourceCompensationDriver) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointSourceCompensationStore)
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
	if p != nil {
		if _, err = store.ReserveProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, *p); err != nil {
			return l, err
		}
	} else if _, err = store.ProjectEnvironmentCloneCheckpointSourceCompensationForLease(ctx, l); err != nil {
		return l, err
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	in, dispatch, err := store.ClaimProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l)
	if err != nil {
		return l, err
	}
	if dispatch {
		if err = s.cloneWorkerAdmission(ctx); err != nil {
			return l, err
		}
		copied, err := cloneCheckpointSourceCompensationPlanCopy(in.Plan)
		if err != nil {
			return l, err
		}
		if err = d.CompensateCheckpointSources(ctx, copied); err != nil {
			return l, errCloneCheckpointUnavailable
		}
	}
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	copied, err := cloneCheckpointSourceCompensationPlanCopy(in.Plan)
	if err != nil {
		return l, err
	}
	observed, err := d.ObserveCheckpointSourceCompensation(ctx, copied)
	if err != nil {
		return l, err
	}
	observed.Sources = append([]state.ProjectEnvironmentCloneCheckpointCompensatedSource(nil), observed.Sources...)
	if err = s.cloneWorkerAdmission(ctx); err != nil {
		return l, err
	}
	return l, store.FinishProjectEnvironmentCloneCheckpointSourceCompensation(ctx, l, observed)
}

func cloneCheckpointSourceCompensationPlanCopy(p state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan) (state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan{}, err
	}
	var out state.ProjectEnvironmentCloneCheckpointSourceCompensationPlan
	err = json.Unmarshal(raw, &out)
	return out, err
}
