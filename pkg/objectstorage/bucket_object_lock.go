package objectstorage

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// BucketObjectLockService keeps owned intent separate from native truth. A
// failed/unsupported read still persists a conservative native-enabled hint.
// It performs no native mutation until versioning and accepted transfers drain.
type BucketObjectLockService struct {
	Store         state.ObjectBucketObjectLockStore
	Provider      BucketObjectLockProvider
	Versioning    BucketVersioningProvider
	BeforeRequest func(context.Context) error
}

func (s BucketObjectLockService) before(ctx context.Context) error {
	if s.BeforeRequest != nil {
		return s.BeforeRequest(ctx)
	}
	return nil
}

func (s BucketObjectLockService) Read(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketObjectLock, api.ObjectBucketObjectLockConfiguration, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketObjectLock{}, api.ObjectBucketObjectLockConfiguration{}, ErrUnsupported
	}
	j, err := s.Store.GetObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		return j, api.ObjectBucketObjectLockConfiguration{}, err
	}
	if err = s.before(ctx); err != nil {
		return j, api.ObjectBucketObjectLockConfiguration{}, err
	}
	native, nativeErr := s.Provider.GetBucketObjectLock(ctx, b.PhysicalName)
	if nativeErr == nil && !native.Valid() {
		nativeErr = ErrUnavailable
	}
	// Settlement survives a canceled client connection; native truth and the
	// permanent enablement latch must not be lost with the request context.
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	j, err = s.Store.ObserveObjectBucketObjectLock(finish, b.AccountID, b.AppID, b.ID, j.Revision, native, nativeErr == nil)
	if err != nil {
		return j, native, err
	}
	return j, native, nativeErr
}

func (s BucketObjectLockService) Request(ctx context.Context, b state.ObjectBucket, c api.ObjectBucketObjectLockConfiguration) (state.ObjectBucketObjectLock, error) {
	if !c.Enabled || !c.Valid() {
		return state.ObjectBucketObjectLock{}, ErrInvalid
	}
	if s.Store == nil || s.Provider == nil || s.Versioning == nil {
		return state.ObjectBucketObjectLock{}, ErrUnsupported
	}
	j, _, err := s.Read(ctx, b)
	if err != nil {
		return j, err
	}
	if err = s.before(ctx); err != nil {
		return j, err
	}
	v, err := s.Versioning.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return j, err
	}
	if v.Status != "" && !state.ValidObjectBucketVersioningStatus(v.Status) {
		return j, ErrUnavailable
	}
	if v.MFADelete == "Enabled" && v.Status != "Enabled" {
		return j, ErrUnsupported
	}
	return s.Store.RequestObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID, c.Clone())
}

func (s BucketObjectLockService) Reconcile(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketObjectLock, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketObjectLock{}, ErrUnsupported
	}
	attempt, cancel := context.WithTimeout(ctx, api.ObjectBucketObjectLockTimeout)
	defer cancel()
	// Refreshing before claiming also repairs the Enabled versioning intent
	// after an overlapping deletion has finished. Reads never replace intent.
	j, native, err := s.Read(attempt, b)
	if err != nil || j.State == "ready" {
		return j, err
	}
	j, err = s.Store.ClaimObjectBucketObjectLock(attempt, b.ID, uuid.NewString())
	if err != nil || j.Token == "" {
		return j, err
	}
	out, err := s.reconcileClaimed(attempt, b, j, native)
	if err != nil {
		finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
		defer finishCancel()
		code := "provider_failed"
		if errors.Is(err, ErrUnsupported) {
			code = "provider_unsupported"
		}
		retryErr := s.Store.RetryObjectBucketObjectLock(finish, b.ID, j.Token, code)
		if retryErr != nil && !errors.Is(retryErr, state.ErrConflict) {
			return j, retryErr
		}
	}
	return out, err
}

func (s BucketObjectLockService) reconcileClaimed(ctx context.Context, b state.ObjectBucket, j state.ObjectBucketObjectLock, native api.ObjectBucketObjectLockConfiguration) (state.ObjectBucketObjectLock, error) {
	if j.DesiredConfiguration != nil && !j.DesiredConfiguration.Equal(native) {
		var err error
		j, err = s.Store.DispatchObjectBucketObjectLock(ctx, b.ID, j.Token)
		if err != nil {
			return j, err
		}
		if err = s.before(ctx); err != nil {
			return j, err
		}
		if err = s.Provider.PutBucketObjectLock(ctx, b.PhysicalName, j.DesiredConfiguration.Clone()); err != nil {
			return j, err
		}
		_, native, err = s.Read(ctx, b)
		if err != nil {
			return j, err
		}
		if !j.DesiredConfiguration.Equal(native) {
			return j, ErrUnavailable
		}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	return s.Store.FinishObjectBucketObjectLock(finish, b.ID, j.Token, native)
}
