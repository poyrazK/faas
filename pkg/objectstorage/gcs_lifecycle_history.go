package objectstorage

import (
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// GCS has no delete markers. IsLatest identifies a live generation, which may
// be absent. DeletedTime, rather than the next generation's metadata timestamp,
// establishes noncurrent age. A complete exact-key history is still required.
func gcsLifecycleHistoryObject(versions []ListedObjectVersion, native string, modified, now time.Time) (LifecycleObject, error) {
	if len(versions) > api.ObjectDeletionHistoryVersions {
		return LifecycleObject{}, ErrUnavailable
	}
	seen, live := map[string]bool{}, false
	var target *ListedObjectVersion
	for i := range versions {
		v := &versions[i]
		_, err := gcsGeneration(v.ProviderVersionID)
		if err != nil || !ValidKey(v.Key) || v.DeleteMarker || seen[v.ProviderVersionID] || v.LastModified.IsZero() || v.LastModified.After(now) || i > 0 && v.Key != versions[0].Key {
			return LifecycleObject{}, ErrUnavailable
		}
		seen[v.ProviderVersionID] = true
		if v.IsLatest {
			if live || !v.NoncurrentSince.IsZero() {
				return LifecycleObject{}, ErrUnavailable
			}
			live = true
		} else if v.NoncurrentSince.IsZero() || v.NoncurrentSince.After(now) {
			return LifecycleObject{}, ErrUnavailable
		}
		if v.ProviderVersionID == native && v.LastModified.Equal(modified) {
			target = v
		}
	}
	if target == nil {
		return LifecycleObject{}, ErrLifecycleNotDue
	}
	// OnlyVersion is the sole-current/expired-marker proof expected by the
	// shared selector. A lone archived generation is still noncurrent.
	o := LifecycleObject{Key: target.Key, LastModified: target.LastModified, IsLatest: target.IsLatest, OnlyVersion: target.IsLatest && len(versions) == 1}
	if !target.IsLatest {
		o.NoncurrentSince = target.NoncurrentSince
		// Metadata can change after archival; it cannot restart or predate the
		// native noncurrent clock used by this action.
		if o.LastModified.After(o.NoncurrentSince) {
			o.LastModified = o.NoncurrentSince
		}
		for _, v := range versions {
			if !v.IsLatest && v.NoncurrentSince.After(target.NoncurrentSince) {
				o.NewerNoncurrent++
			}
		}
	}
	return o, nil
}
