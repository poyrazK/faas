package objectstorage

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"time"
)

type BucketVersioning struct{ Status, MFADelete string }
type BucketVersioningProvider interface {
	GetBucketVersioning(context.Context, string) (BucketVersioning, error)
	PutBucketVersioning(context.Context, string, string) error
}

// BucketVersioningService records intent and observes each single-attempt
// request. Its worker never switches targets while a preceding cutover is busy.
type BucketVersioningService struct {
	Store         state.ObjectBucketVersioningStore
	Provider      BucketVersioningProvider
	BeforeRequest func(context.Context) error
}

func (s BucketVersioningService) before(ctx context.Context) error {
	if s.BeforeRequest != nil {
		return s.BeforeRequest(ctx)
	}
	return nil
}
func (s BucketVersioningService) Read(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketVersioning, BucketVersioning, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketVersioning{}, BucketVersioning{}, ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return state.ObjectBucketVersioning{}, BucketVersioning{}, err
	}
	v, err := s.Provider.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return state.ObjectBucketVersioning{}, v, err
	}
	if v.Status != "" && !state.ValidObjectBucketVersioningStatus(v.Status) {
		return state.ObjectBucketVersioning{}, v, ErrUnavailable
	}
	j, err := s.Store.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, v.Status)
	return j, v, err
}
func (s BucketVersioningService) Request(ctx context.Context, b state.ObjectBucket, status string) (state.ObjectBucketVersioning, error) {
	if !state.ValidObjectBucketVersioningStatus(status) {
		return state.ObjectBucketVersioning{}, ErrInvalid
	}
	_, v, err := s.Read(ctx, b)
	if err != nil {
		return state.ObjectBucketVersioning{}, err
	}
	if v.MFADelete == "Enabled" {
		return state.ObjectBucketVersioning{}, ErrUnsupported
	}
	return s.Store.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, status)
}
func (s BucketVersioningService) Reconcile(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketVersioning, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketVersioning{}, ErrUnsupported
	}
	attemptCtx, cancel := context.WithTimeout(ctx, api.ObjectBucketVersioningOperationTimeout)
	defer cancel()
	j, err := s.Store.ClaimObjectBucketVersioning(attemptCtx, b.ID, uuid.NewString())
	if err != nil {
		return j, err
	}
	if j.Token == "" {
		return j, nil
	}
	result, err := s.reconcileClaimed(attemptCtx, b, j)
	if err != nil {
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
		defer cancel()
		e := s.Store.RetryObjectBucketVersioning(finishCtx, b.ID, j.Token)
		if e != nil && !errors.Is(e, state.ErrConflict) {
			return j, e
		}
	}
	return result, err
}
func (s BucketVersioningService) reconcileClaimed(ctx context.Context, b state.ObjectBucket, j state.ObjectBucketVersioning) (state.ObjectBucketVersioning, error) {
	if err := s.before(ctx); err != nil {
		return j, err
	}
	v, err := s.Provider.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return j, err
	}
	if v.MFADelete == "Enabled" && v.Status != j.DesiredStatus {
		return j, ErrUnsupported
	}
	if v.Status != j.DesiredStatus && j.State != "inventory" {
		if err = s.before(ctx); err != nil {
			return j, err
		}
		j, err = s.Store.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
		if err != nil {
			return j, err
		}
		if err = s.Provider.PutBucketVersioning(ctx, b.PhysicalName, j.DesiredStatus); err != nil {
			return j, err
		}
		if err = s.before(ctx); err != nil {
			return j, err
		}
		v, err = s.Provider.GetBucketVersioning(ctx, b.PhysicalName)
		if err != nil {
			return j, err
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	return s.Store.AdvanceObjectBucketVersioning(finishCtx, b.ID, j.Token, v.Status)
}

// Configuration reads count as provider requests alongside object operations.
func VersioningRequestRecorder(metrics state.ObjectStorageProviderUsageStore, bucket string) func(context.Context) error {
	return func(ctx context.Context) error {
		if metrics == nil {
			return ErrConfiguration
		}
		return metrics.RecordObjectStorageProviderRequest(ctx, bucket, time.Now().UTC())
	}
}
