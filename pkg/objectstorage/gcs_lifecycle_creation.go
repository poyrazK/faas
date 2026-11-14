package objectstorage

import (
	"context"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// XML LastModified is mutable metadata time. A generation-pinned JSON read
// supplies immutable timeCreated; the metadata timestamp must still match the
// discovery identity. Disable native retries so every attempt is metered.
func (s *googleGCSStore) ObjectVersionCreationTime(ctx context.Context, bucket, key string, generation int64, modified time.Time) (time.Time, error) {
	a, err := s.client.Bucket(bucket).Object(key).Generation(generation).Retryer(storage.WithPolicy(storage.RetryNever)).Attrs(ctx)
	if err != nil {
		return time.Time{}, err
	}
	if a == nil || a.Name != key || a.Generation != generation || a.Created.IsZero() || a.Updated.IsZero() || a.Created.After(a.Updated) {
		return time.Time{}, ErrUnavailable
	}
	if !a.Updated.Equal(modified) {
		return time.Time{}, ErrLifecycleNotDue
	}
	return a.Created, nil
}

func (s DeletionService) lifecycleObject(ctx context.Context, b state.ObjectBucket, versions []ListedObjectVersion, native string, modified, now time.Time, rules []api.ObjectLifecycleRule) (LifecycleObject, error) {
	o, err := s.lifecycleHistoryObject(versions, native, modified, now)
	if err != nil {
		return o, err
	}
	p, gcs := s.Provider.(*GCS)
	if !gcs || !o.IsLatest || !gcsLifecycleNeedsCreation(rules, o.Key) {
		return o, nil
	}
	reader, ok := p.store.(interface {
		ObjectVersionCreationTime(context.Context, string, string, int64, time.Time) (time.Time, error)
	})
	if !ok {
		return LifecycleObject{}, ErrUnsupported
	}
	generation, err := gcsGeneration(native)
	if err != nil {
		return LifecycleObject{}, ErrUnavailable
	}
	if err = s.before(ctx); err != nil {
		return LifecycleObject{}, err
	}
	created, err := reader.ObjectVersionCreationTime(ctx, b.PhysicalName, o.Key, generation, modified)
	if err != nil {
		return LifecycleObject{}, normalizeGCS(err)
	}
	if created.IsZero() || created.After(modified) || created.After(now) {
		return LifecycleObject{}, ErrUnavailable
	}
	// Only the eligibility clock changes. Deletion receipts and preflight
	// continue to bind the original native generation and metadata timestamp.
	o.LastModified = created
	return o, nil
}

func gcsLifecycleNeedsCreation(rules []api.ObjectLifecycleRule, key string) bool {
	for _, r := range rules {
		if r.Status == "Enabled" && strings.HasPrefix(key, r.Filter.Prefix) && r.Expiration != nil && r.Expiration.Days != nil {
			return true
		}
	}
	return false
}
