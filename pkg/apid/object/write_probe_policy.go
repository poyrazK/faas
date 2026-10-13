package object

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
)

// Do not deliberately create a temporary object in a bucket where its cleanup
// may be prohibited by retention. This preflight does not bypass or freeze a
// provider's policy; cleanup still has to succeed at execution time.
func (s *Service) WriteProbeAllowed(ctx context.Context, accountID, appID, name, scope string) (bool, error) {
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	locks, lockOK := s.Host.Store().(state.ObjectBucketObjectLockStore)
	if !ok || !lockOK {
		return false, errors.New("object-storage cleanup policy unavailable")
	}
	rows, err := buckets.ListObjectBuckets(ctx, accountID, appID)
	if err != nil {
		return false, err
	}
	for _, bucket := range rows {
		if bucket.Name != name || bucket.Scope != scope || bucket.State != "ready" {
			continue
		}
		lock, err := locks.GetObjectBucketObjectLock(ctx, accountID, appID, bucket.ID)
		if err != nil {
			return false, err
		}
		return lock.State == "ready" && !lock.EnabledRequired && !lock.NativeEnabledObserved &&
			(lock.DesiredConfiguration == nil || !lock.DesiredConfiguration.Enabled) &&
			(lock.ObservedConfiguration == nil || !lock.ObservedConfiguration.Enabled), nil
	}
	return false, nil
}
