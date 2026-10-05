package objectstorage

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A policy is customer intent, not autonomous provider configuration. Removing
// it cancels unclaimed discovery; already admitted journals retain recovery.
type LifecyclePolicyService struct {
	Store    state.ObjectLifecycleStore
	Provider Provider
}

func (s LifecyclePolicyService) Read(ctx context.Context, b state.ObjectBucket) (state.ObjectLifecyclePolicy, error) {
	if s.Store == nil {
		return state.ObjectLifecyclePolicy{}, ErrUnsupported
	}
	return s.Store.GetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID)
}
func (s LifecyclePolicyService) Write(ctx context.Context, b state.ObjectBucket, rules []api.ObjectLifecycleRule) (state.ObjectLifecyclePolicy, error) {
	if s.Store == nil {
		return state.ObjectLifecyclePolicy{}, ErrUnsupported
	}
	valid, err := api.NormalizeObjectLifecycleRules(rules)
	if err != nil {
		return state.ObjectLifecyclePolicy{}, ErrInvalid
	}
	for _, r := range valid {
		if r.Expiration != nil || r.NoncurrentVersionExpiration != nil {
			if _, ok := s.Provider.(ObjectVersionLister); !ok {
				return state.ObjectLifecyclePolicy{}, ErrUnsupported
			}
		}
		if len(r.Filter.Tags) != 0 {
			if _, ok := s.Provider.(ObjectVersionTagger); !ok {
				return state.ObjectLifecyclePolicy{}, ErrUnsupported
			}
		}
		if r.AbortIncompleteMultipartDays != nil && s.Provider == nil {
			return state.ObjectLifecyclePolicy{}, ErrUnsupported
		}
	}
	return s.Store.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, valid)
}
