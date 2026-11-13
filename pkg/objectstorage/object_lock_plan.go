package objectstorage

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ErrObjectLockPlan marks Object Lock protection the bucket owner's plan does
// not include (ADR-20261111123000 decision 8). It is wrapped in a
// state.ObjectStorageLimitError naming the limit and the observed value.
var ErrObjectLockPlan = errors.New("object lock protection exceeds the plan")

// CheckObjectLockPlan reads the limits of the plan that pays for b from store
// and refuses the protection check finds beyond them. A store that cannot
// read plan limits (a narrow test fake) is not checked; every production
// store can.
func CheckObjectLockPlan(ctx context.Context, store any, b state.ObjectBucket, check func(api.ObjectStoragePlanLimits) *api.ObjectLockPlanViolation) error {
	st, ok := store.(state.ObjectBucketPlanLimitsStore)
	if !ok {
		return nil
	}
	limits, err := st.ObjectBucketPlanLimits(ctx, b.AccountID, b.ID)
	if err != nil {
		return err
	}
	return ObjectLockPlanError(check(limits))
}

// ObjectLockPlanError converts a plan violation to an error, nil for none.
func ObjectLockPlanError(v *api.ObjectLockPlanViolation) error {
	if v == nil {
		return nil
	}
	return &state.ObjectStorageLimitError{Kind: v.Kind, Limit: v.Limit, Observed: v.Observed, Cause: ErrObjectLockPlan}
}
