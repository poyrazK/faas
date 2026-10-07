package objectstorage

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// NativeBucketEncryption is an internal provider observation. Native key IDs
// never form customer-facing encryption selections.
type NativeBucketEncryption struct {
	Algorithm              string   `json:"-"`
	KeyID                  string   `json:"-"`
	BucketKeyEnabled       *bool    `json:"-"`
	BlockedEncryptionTypes []string `json:"-"`
}

func (n NativeBucketEncryption) Clone() NativeBucketEncryption {
	n.BlockedEncryptionTypes = slices.Clone(n.BlockedEncryptionTypes)
	if n.BucketKeyEnabled != nil {
		value := *n.BucketKeyEnabled
		n.BucketKeyEnabled = &value
	}
	return n
}

func (n NativeBucketEncryption) Matches(e ResolvedObjectEncryption) bool {
	if e.Empty() {
		return (n.Algorithm == "" || n.Algorithm == "AES256") && n.KeyID == "" && (n.BucketKeyEnabled == nil || !*n.BucketKeyEnabled)
	}
	if n.Algorithm != e.Selection.Algorithm || n.KeyID != e.ProviderKeyID {
		return false
	}
	if e.Selection.BucketKeyEnabled == nil {
		return n.BucketKeyEnabled == nil || !*n.BucketKeyEnabled
	}
	return (n.BucketKeyEnabled != nil && *n.BucketKeyEnabled) == *e.Selection.BucketKeyEnabled
}

// Mutations must invoke beforeEncryptionWrite from their context after key
// validation and immediately before the single native attempt. The service
// uses that callback to fence the lease and meter the dispatched request.
type BucketEncryptionProvider interface {
	GetBucketEncryption(context.Context, string) (NativeBucketEncryption, error)
	PutBucketEncryption(context.Context, string, ResolvedObjectEncryption, NativeBucketEncryption) error
	ClearBucketEncryption(context.Context, string, NativeBucketEncryption) error
}

// BucketEncryptionService publishes only configurations with exact native
// readback. A restarted worker observes an accepted mutation before retrying it.
type BucketEncryptionService struct {
	Store         state.ObjectBucketEncryptionStore
	Provider      BucketEncryptionProvider
	BeforeRequest func(context.Context) error
}

func (s BucketEncryptionService) before(ctx context.Context) error {
	if s.BeforeRequest != nil {
		return s.BeforeRequest(ctx)
	}
	return nil
}

func (s BucketEncryptionService) Request(ctx context.Context, b state.ObjectBucket, e ResolvedObjectEncryption) (state.ObjectBucketEncryption, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketEncryption{}, ErrUnsupported
	}
	if !e.ValidFor(b.AccountID) || e.Selection.Context != "" {
		return state.ObjectBucketEncryption{}, ErrInvalid
	}
	j, err := s.Store.RequestObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, e)
	if err != nil || j.State != "ready" {
		return j, err
	}
	j, _, err = s.Read(ctx, b)
	return j, err
}

// Read detects drift only after Gregale has accepted ownership of the policy.
// Provider settings on an unenrolled bucket are observed without replacing them.
func (s BucketEncryptionService) Read(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketEncryption, NativeBucketEncryption, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketEncryption{}, NativeBucketEncryption{}, ErrUnsupported
	}
	j, err := s.Store.GetObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		return j, NativeBucketEncryption{}, err
	}
	if err = s.before(ctx); err != nil {
		return j, NativeBucketEncryption{}, err
	}
	native, err := s.Provider.GetBucketEncryption(ctx, b.PhysicalName)
	if err != nil {
		return j, native, err
	}
	if j.State == "ready" && j.Revision > 0 && !native.Matches(j.Encryption) {
		j, err = s.Store.RefreshObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID, j.Revision)
		if errors.Is(err, state.ErrConflict) {
			j, err = s.Store.GetObjectBucketEncryption(ctx, b.AccountID, b.AppID, b.ID)
		}
	}
	return j, native, err
}

func (s BucketEncryptionService) Reconcile(ctx context.Context, b state.ObjectBucket) (state.ObjectBucketEncryption, error) {
	if s.Store == nil || s.Provider == nil {
		return state.ObjectBucketEncryption{}, ErrUnsupported
	}
	attempt, cancel := context.WithTimeout(ctx, api.ObjectBucketEncryptionOperationTimeout)
	defer cancel()
	attempt = WithEncryptionRequestRecorder(attempt, s.before)
	if _, err := s.Store.GetObjectBucketEncryption(attempt, b.AccountID, b.AppID, b.ID); err != nil {
		return state.ObjectBucketEncryption{}, err
	}
	j, err := s.Store.ClaimObjectBucketEncryption(attempt, b.ID, uuid.NewString())
	if err != nil {
		return j, err
	}
	out, err := s.reconcileClaimed(attempt, b, j)
	if err != nil {
		finish, finishCancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
		defer finishCancel()
		retryErr := s.Store.RetryObjectBucketEncryption(finish, b.ID, j.Token)
		if retryErr != nil && !errors.Is(retryErr, state.ErrConflict) {
			return j, retryErr
		}
	}
	return out, err
}

func (s BucketEncryptionService) reconcileClaimed(ctx context.Context, b state.ObjectBucket, j state.ObjectBucketEncryption) (state.ObjectBucketEncryption, error) {
	if err := s.before(ctx); err != nil {
		return j, err
	}
	native, err := s.Provider.GetBucketEncryption(ctx, b.PhysicalName)
	if err != nil {
		return j, err
	}
	if !native.Matches(j.DesiredEncryption) {
		mutationCtx := WithEncryptionWriteRecorder(ctx, func(callCtx context.Context) error {
			var dispatchErr error
			j, dispatchErr = s.Store.DispatchObjectBucketEncryption(callCtx, b.ID, j.Token)
			if dispatchErr != nil {
				return dispatchErr
			}
			return s.before(callCtx)
		})
		if j.DesiredEncryption.Empty() {
			err = s.Provider.ClearBucketEncryption(mutationCtx, b.PhysicalName, native)
		} else {
			err = s.Provider.PutBucketEncryption(mutationCtx, b.PhysicalName, j.DesiredEncryption, native)
		}
		if err != nil {
			return j, err
		}
		if err = s.before(ctx); err != nil {
			return j, err
		}
		verified, e := s.Provider.GetBucketEncryption(ctx, b.PhysicalName)
		if e != nil {
			return j, e
		}
		if !verified.Matches(j.DesiredEncryption) || !slices.Equal(native.BlockedEncryptionTypes, verified.BlockedEncryptionTypes) {
			return j, ErrUnavailable
		}
	}
	finish, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	return s.Store.FinishObjectBucketEncryption(finish, b.ID, j.Token, j.DesiredEncryption)
}
