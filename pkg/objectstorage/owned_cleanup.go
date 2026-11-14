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

// OwnedCleanupOptions select what a cleanup pass may remove.
type OwnedCleanupOptions struct {
	// ReleaseProtection lets the platform remove the protection it can:
	// legal holds and event holds are released and GOVERNANCE versions are
	// deleted with the governance bypass. Only account erasure and
	// over-ceiling deletion set it (ADR-20261111123000 decision 8); preview
	// and clone teardown keep every protected version.
	ReleaseProtection bool
}

// ProtectedUntilError reports versions a cleanup pass had to keep after
// deleting everything else it reached: COMPLIANCE retention, or protection the
// pass may not release. Until is the earliest retain-until among them, and
// Undated reports one kept by a hold, which has no date.
type ProtectedUntilError struct {
	Kept    int
	Until   time.Time
	Undated bool
}

func (e *ProtectedUntilError) Error() string { return ErrObjectProtected.Error() }
func (e *ProtectedUntilError) Unwrap() error { return ErrObjectProtected }

func (e *ProtectedUntilError) keep(until time.Time) {
	e.Kept++
	if until.IsZero() {
		e.Undated = true
	} else if e.Until.IsZero() || until.Before(e.Until) {
		e.Until = until
	}
}

// RetryAfter is when the pass should run again: once the earliest kept
// retention ends, between api.ObjectOwnedCleanupProtectedRetryMin and
// api.ObjectOwnedCleanupProtectedRetryMax. A hold may end at any time, so a
// version kept by one is checked again after the minimum.
func (e *ProtectedUntilError) RetryAfter(now time.Time) time.Duration {
	d := api.ObjectOwnedCleanupProtectedRetryMin
	if !e.Undated && !e.Until.IsZero() {
		d = e.Until.Sub(now) + time.Minute
	}
	return min(max(d, api.ObjectOwnedCleanupProtectedRetryMin), api.ObjectOwnedCleanupProtectedRetryMax)
}

// CleanupOwnedBucketObjects performs one bounded batch that releases no
// protection. See CleanupOwnedBucketObjectsWith.
func CleanupOwnedBucketObjects(ctx context.Context, provider Provider, bucket string) error {
	return CleanupOwnedBucketObjectsWith(ctx, provider, bucket, OwnedCleanupOptions{})
}

// CleanupOwnedBucketObjectsWith performs one bounded batch. Its caller must own a
// durable deleting fence, drain accepted writes and keep this physical bucket
// sealed until DeleteBucket is acknowledged. A retry starts at the first page:
// deleted versions disappear, so progress survives a crash without a mutable
// pagination cursor. No absence observation completes the bucket journal.
// Protected versions do not stop the pass: it deletes what it may, keeps the
// rest and returns a *ProtectedUntilError once nothing else is in reach.
func CleanupOwnedBucketObjectsWith(ctx context.Context, provider Provider, bucket string, opts OwnedCleanupOptions) error {
	versioned, protected, err := ownedCleanupConfiguration(ctx, provider, bucket)
	if err != nil {
		return err
	}
	if versioned || protected {
		return cleanupOwnedVersions(ctx, provider, bucket, protected, opts)
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

// ownedVersionAction is what cleanup does with one version.
type ownedVersionAction int

const (
	ownedVersionDelete ownedVersionAction = iota
	ownedVersionBypass
	ownedVersionKeep
)

func cleanupOwnedVersions(ctx context.Context, provider Provider, bucket string, protected bool, opts OwnedCleanupOptions) error {
	lister, ok := provider.(ObjectVersionInventoryProvider)
	deleter, deleteOK := provider.(ObjectVersionDeleter)
	if !ok || !deleteOK {
		return ErrUnsupported
	}
	kept := &ProtectedUntilError{}
	cursor := ""
	for pages := 1; ; pages++ {
		page, err := lister.ListObjectVersions(ctx, bucket, cursor, api.ObjectOwnedCleanupBatchSize)
		if err != nil {
			return err
		}
		if len(page.Items) > api.ObjectOwnedCleanupBatchSize || len(page.Items) == 0 && page.NextCursor != "" {
			return ErrUnavailable
		}
		if err := validOwnedVersionPage(provider, page); err != nil {
			return err
		}
		deleted := 0
		for _, v := range page.Items {
			action := ownedVersionDelete
			if protected && !v.DeleteMarker {
				var until time.Time
				if action, until, err = ownedVersionProtection(ctx, provider, bucket, v, opts.ReleaseProtection); err != nil {
					return err
				}
				if action == ownedVersionKeep {
					kept.keep(until)
					continue
				}
			}
			if err := deleteOwnedVersion(ctx, provider, deleter, bucket, v, action == ownedVersionBypass); err != nil {
				return err
			}
			deleted++
		}
		switch {
		case page.NextCursor == "" && kept.Kept > 0:
			return kept
		case page.NextCursor == "":
			return nil
		case deleted > 0:
			return ErrCleanupPending
		case pages >= api.ObjectOwnedCleanupProtectedPages:
			// Every version in reach must be kept. Wait for the earliest
			// to end instead of rereading them every few seconds.
			return kept
		}
		cursor = page.NextCursor
	}
}

// validOwnedVersionPage validates every identity before removing evidence.
func validOwnedVersionPage(provider Provider, page ObjectVersionsPage) error {
	seen := map[string]bool{}
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
	return nil
}

func deleteOwnedVersion(ctx context.Context, provider Provider, deleter ObjectVersionDeleter, bucket string, v ObjectVersionInventoryEntry, bypass bool) error {
	switch {
	case v.ProviderVersionID == "null":
		// Only sealed recursive cleanup can safely repeat a mutable null
		// deletion. It never releases the fence back to future writers.
		result, err := provider.(MutableObjectDeleter).DeleteMutableObject(ctx, bucket, v.Key, "null")
		if err != nil {
			return err
		}
		if result.ProviderVersionID != "null" {
			return ErrUnavailable
		}
		return nil
	case bypass:
		_, err := provider.(GovernanceBypassDeleter).DeleteObjectVersionBypassingGovernance(ctx, bucket, v.Key, v.ProviderVersionID)
		return err
	}
	_, err := deleter.DeleteObjectVersion(ctx, bucket, v.Key, v.ProviderVersionID)
	return err
}

// ownedVersionProtection reads a version's protection just before its exact
// deletion, so a slow provider still makes bounded progress. With release it
// turns off legal and event holds and bypasses GOVERNANCE retention; it never
// shortens COMPLIANCE retention. A kept version returns its retain-until, or
// zero when a hold keeps it.
func ownedVersionProtection(ctx context.Context, provider Provider, bucket string, v ObjectVersionInventoryEntry, release bool) (ownedVersionAction, time.Time, error) {
	lock, ok := provider.(ObjectVersionLockProvider)
	if !ok {
		return ownedVersionKeep, time.Time{}, ErrUnsupported
	}
	hold, err := lock.GetObjectVersionLegalHold(ctx, bucket, v.Key, v.ProviderVersionID)
	if err != nil {
		return ownedVersionKeep, time.Time{}, err
	}
	if !hold.Valid() {
		return ownedVersionKeep, time.Time{}, ErrUnavailable
	}
	retention, err := lock.GetObjectVersionRetention(ctx, bucket, v.Key, v.ProviderVersionID)
	if err != nil {
		return ownedVersionKeep, time.Time{}, err
	}
	if !retention.Valid() {
		return ownedVersionKeep, time.Time{}, ErrUnavailable
	}
	if (hold.Status == "ON" || retention.EventHold == "ON") && !release {
		return ownedVersionKeep, time.Time{}, nil
	}
	if hold.Status == "ON" {
		if err := lock.PutObjectVersionLegalHold(ctx, bucket, v.Key, v.ProviderVersionID, api.ObjectVersionLegalHold{Status: "OFF"}); err != nil {
			return ownedVersionKeep, time.Time{}, err
		}
	}
	if retention.EventHold == "ON" {
		// Releasing an event hold starts its retention period; the
		// provider dates it, so read it back.
		off := api.ObjectVersionRetention{Mode: retention.Mode, EventHold: "OFF"}
		if err := lock.PutObjectVersionRetention(ctx, bucket, v.Key, v.ProviderVersionID, off, retention.Mode == "GOVERNANCE"); err != nil {
			return ownedVersionKeep, time.Time{}, err
		}
		if retention, err = lock.GetObjectVersionRetention(ctx, bucket, v.Key, v.ProviderVersionID); err != nil {
			return ownedVersionKeep, time.Time{}, err
		}
		if !retention.Valid() || retention.EventHold == "ON" {
			return ownedVersionKeep, time.Time{}, ErrUnavailable
		}
	}
	if retention.RetainUntilDate == nil || !retention.RetainUntilDate.After(time.Now()) {
		return ownedVersionDelete, time.Time{}, nil
	}
	if _, bypass := provider.(GovernanceBypassDeleter); release && bypass && retention.Mode == "GOVERNANCE" && v.ProviderVersionID != "null" {
		return ownedVersionBypass, time.Time{}, nil
	}
	return ownedVersionKeep, *retention.RetainUntilDate, nil
}
