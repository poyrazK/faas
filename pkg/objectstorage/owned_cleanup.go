package objectstorage

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var (
	ErrCleanupPending  = errors.New("owned bucket cleanup has more work")
	ErrObjectProtected = errors.New("owned bucket cleanup is blocked by object protection")
)

// CleanupOwnedBucketObjects performs one bounded batch. Its caller must own a
// durable deleting fence, drain accepted writes and keep this physical bucket
// sealed until DeleteBucket is acknowledged. A retry starts at the first page:
// deleted versions disappear, so progress survives a crash without a mutable
// pagination cursor. No absence observation completes the bucket journal.
func CleanupOwnedBucketObjects(ctx context.Context, provider Provider, bucket string) error {
	versioned, protected, err := ownedCleanupConfiguration(ctx, provider, bucket)
	if err != nil {
		return err
	}
	if versioned || protected {
		return cleanupOwnedVersions(ctx, provider, bucket, protected)
	}
	page, err := provider.ListObjects(ctx, bucket, "", "", api.ObjectOwnedCleanupBatchSize)
	if err != nil {
		return err
	}
	if len(page.Items) > api.ObjectOwnedCleanupBatchSize || len(page.CommonPrefixes) != 0 || len(page.Items) == 0 && page.NextCursor != "" {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		if !ValidKey(item.Key) || seen[item.Key] || item.Size < 0 || item.Size > api.MaxObjectUploadBytes {
			return ErrUnavailable
		}
		seen[item.Key] = true
	}
	for _, item := range page.Items {
		if err := provider.DeleteObject(ctx, bucket, item.Key); err != nil {
			return err
		}
	}
	if page.NextCursor != "" {
		return ErrCleanupPending
	}
	return nil
}

func ownedCleanupConfiguration(ctx context.Context, provider Provider, bucket string) (bool, bool, error) {
	var versioned, protected bool
	if native, ok := provider.(BucketVersioningProvider); ok {
		v, err := native.GetBucketVersioning(ctx, bucket)
		if err != nil {
			return false, false, err
		}
		if v.Status != "" && v.Status != "Enabled" && v.Status != "Suspended" || v.MFADelete != "" && v.MFADelete != "Disabled" {
			return false, false, ErrUnsupported
		}
		versioned = v.Status != ""
	}
	if native, ok := provider.(BucketObjectLockProvider); ok {
		lock, err := native.GetBucketObjectLock(ctx, bucket)
		if err != nil {
			return false, false, err
		}
		if !lock.Valid() {
			return false, false, ErrUnavailable
		}
		protected = lock.Enabled
	}
	return versioned, protected, nil
}

func cleanupOwnedVersions(ctx context.Context, provider Provider, bucket string, protected bool) error {
	lister, ok := provider.(ObjectVersionInventoryProvider)
	deleter, deleteOK := provider.(ObjectVersionDeleter)
	if !ok || !deleteOK {
		return ErrUnsupported
	}
	page, err := lister.ListObjectVersions(ctx, bucket, "", api.ObjectOwnedCleanupBatchSize)
	if err != nil {
		return err
	}
	if len(page.Items) > api.ObjectOwnedCleanupBatchSize || len(page.Items) == 0 && page.NextCursor != "" {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	// Validate every identity before removing evidence. Read protection just
	// before each exact deletion so a slow provider still makes bounded progress.
	for _, v := range page.Items {
		identity := v.Key + "\x00" + v.ProviderVersionID
		if !ValidKey(v.Key) || !validNativeVersionID(v.ProviderVersionID) || v.SizeBytes < 0 || v.SizeBytes > api.MaxObjectUploadBytes || seen[identity] {
			return ErrUnavailable
		}
		seen[identity] = true
		if v.ProviderVersionID == "null" {
			if _, ok := provider.(MutableObjectDeleter); !ok {
				return ErrUnsupported
			}
		}
	}
	for _, v := range page.Items {
		if protected && !v.DeleteMarker {
			if err := ownedVersionProtection(ctx, provider, bucket, v); err != nil {
				return err
			}
		}
		if v.ProviderVersionID == "null" {
			// Only sealed recursive cleanup can safely repeat a mutable null
			// deletion. It never releases the fence back to future writers.
			result, err := provider.(MutableObjectDeleter).DeleteMutableObject(ctx, bucket, v.Key, "null")
			if err != nil {
				return err
			}
			if result.ProviderVersionID != "null" {
				return ErrUnavailable
			}
		} else if _, err := deleter.DeleteObjectVersion(ctx, bucket, v.Key, v.ProviderVersionID); err != nil {
			return err
		}
	}
	if page.NextCursor != "" {
		return ErrCleanupPending
	}
	return nil
}

func ownedVersionProtection(ctx context.Context, provider Provider, bucket string, v ObjectVersionInventoryEntry) error {
	lock, ok := provider.(ObjectVersionLockProvider)
	if !ok {
		return ErrUnsupported
	}
	hold, err := lock.GetObjectVersionLegalHold(ctx, bucket, v.Key, v.ProviderVersionID)
	if err != nil {
		return err
	}
	if !hold.Valid() {
		return ErrUnavailable
	}
	retention, err := lock.GetObjectVersionRetention(ctx, bucket, v.Key, v.ProviderVersionID)
	if err != nil {
		return err
	}
	if !retention.Valid() {
		return ErrUnavailable
	}
	if hold.Status == "ON" || retention.EventHold == "ON" || retention.RetainUntilDate != nil && retention.RetainUntilDate.After(time.Now()) {
		return ErrObjectProtected
	}
	return nil
}
