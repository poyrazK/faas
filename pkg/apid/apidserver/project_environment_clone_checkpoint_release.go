package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// Qualified providers release only original operation-owned native barriers,
// preserving immutable snapshots and object versions. The stable request key is
// state.ProjectEnvironmentCloneCheckpointReleaseRequestID(plan.OperationID).
// Observe reads the original native terminal journal and independently verifies
// the exact original captures remain retained through retainUntil. It must never
// dispatch, retry, recapture, regrant, or inspect a replacement source plan.
// Complete writer coverage belongs to the original checkpoint; reopened sources
// need not be closed again. There is no built-in qualified release driver.
type cloneCheckpointReleaseDriver interface {
	cloneCheckpointAdoptionDriver
	ReleaseCapturedSources(context.Context, cloneCommonCheckpointPlan, cloneCommonCheckpointReceipt) error
	ObserveCapturedSourceRelease(context.Context, cloneCommonCheckpointPlan, cloneCommonCheckpointReceipt, time.Time) (state.ProjectEnvironmentCloneCheckpointReleaseObservation, error)
}

func cloneCheckpointReceiptCopy(in cloneCommonCheckpointReceipt) cloneCommonCheckpointReceipt {
	in.Sources = append([]cloneCommonCheckpointSourceReceipt(nil), in.Sources...)
	return in
}

// Successful release is separate from compensation. Local configuration and
// write holds remain owned until independent native release/retention evidence
// commits atomically with the private release receipt. Lost acknowledgements
// authorize observation only, even across worker handoff and source mutation.
func (s *server) releaseProjectEnvironmentCloneCheckpoint(ctx context.Context, lease state.ProjectEnvironmentCloneLease, driver cloneCheckpointReleaseDriver, bucketLimit int) (state.ProjectEnvironmentCloneLease, error) {
	store, ok := s.store.(state.ProjectEnvironmentCloneCheckpointReleaseStore)
	common, commonOK := s.store.(cloneCommonCheckpointStore)
	if !ok || !commonOK || driver == nil || s.cloneWorkerAdmission == nil {
		return lease, errCloneCheckpointUnavailable
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	release, err := store.ProjectEnvironmentCloneCheckpointReleaseForLease(ctx, lease)
	if errors.Is(err, state.ErrNotFound) {
		lease, err = s.adoptProjectEnvironmentCloneCheckpoint(ctx, lease, driver, bucketLimit)
		if err != nil {
			return lease, err
		}
		ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
		defer cancel()
		plan, _, _, planErr := s.cloneCommonCheckpointPlan(ctx, lease, common)
		if planErr != nil {
			return lease, planErr
		}
		receipt, observeErr := driver.Observe(ctx, cloneCommonCheckpointPlanCopy(plan))
		if observeErr != nil {
			return lease, observeErr
		}
		if validateCloneCommonCheckpointReceipt(plan, receipt) != nil {
			return lease, state.ErrConflict
		}
		if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, plan); err != nil {
			return lease, err
		}
		release, err = store.ReserveProjectEnvironmentCloneCheckpointRelease(ctx, lease, plan, receipt)
	}
	if err != nil {
		return lease, err
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	if release.State == "planned" {
		if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, release.Plan); err != nil {
			return lease, err
		}
		// Recheck native closure and original retained identities immediately before
		// claiming dispatch; a planned row alone is not native release authority.
		receipt, err := driver.Observe(ctx, cloneCommonCheckpointPlanCopy(release.Plan))
		if err != nil {
			return lease, err
		}
		if cloneCheckpointDigest(receipt) != cloneCheckpointDigest(release.Receipt) {
			return lease, state.ErrConflict
		}
		var dispatch bool
		release, dispatch, err = store.ClaimProjectEnvironmentCloneCheckpointRelease(ctx, lease)
		if err != nil {
			return lease, err
		}
		if dispatch {
			if err := s.checkCloneCommonCheckpointHolds(ctx, lease, common, release.Plan); err != nil {
				return lease, err
			}
			if err := driver.ReleaseCapturedSources(ctx, cloneCommonCheckpointPlanCopy(release.Plan), cloneCheckpointReceiptCopy(release.Receipt)); err != nil {
				return lease, err
			}
		}
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	observed, err := driver.ObserveCapturedSourceRelease(ctx, cloneCommonCheckpointPlanCopy(release.Plan), cloneCheckpointReceiptCopy(release.Receipt), lease.ExpiresAt)
	if err != nil {
		return lease, err
	}
	if err := s.cloneWorkerAdmission(ctx); err != nil {
		return lease, err
	}
	updated, err := store.FinishProjectEnvironmentCloneCheckpointRelease(ctx, lease, observed)
	if err != nil {
		return lease, err
	}
	lease.Operation = updated
	return lease, nil
}
