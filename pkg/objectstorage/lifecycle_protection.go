package objectstorage

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Permanent lifecycle deletion is permitted only in the permanently versioned
// profile captured at admission. Markers have no per-version WORM policy.
func (s DeletionService) lifecycleProtectionClear(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) error {
	if !j.ProtectionRequired || j.Lifecycle == nil || j.Lifecycle.ExpectedDeleteMarker == nil || j.Selector == "" || j.Lifecycle.ExpectedProviderVersionID != protectedLifecycleNativeID(j) {
		return ErrUnavailable
	}
	if err := s.lifecycleProtectionConfiguration(ctx, b); err != nil {
		return err
	}
	if *j.Lifecycle.ExpectedDeleteMarker {
		return nil
	}
	p, ok := s.Provider.(ObjectVersionLockProvider)
	if !ok {
		return ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return err
	}
	hold, err := p.GetObjectVersionLegalHold(ctx, b.PhysicalName, j.Key, protectedLifecycleNativeID(j))
	if err != nil {
		return err
	}
	if !hold.Valid() {
		return ErrUnavailable
	}
	if hold.Status == "ON" {
		return ErrObjectProtected
	}
	if err = s.before(ctx); err != nil {
		return err
	}
	r, err := p.GetObjectVersionRetention(ctx, b.PhysicalName, j.Key, protectedLifecycleNativeID(j))
	if err != nil {
		return err
	}
	if !r.Valid() || r.EventHold == "OFF" && r.RetainUntilDate == nil {
		return ErrUnavailable
	}
	if r.EventHold == "ON" || r.RetainUntilDate != nil && r.RetainUntilDate.After(time.Now().UTC()) {
		return ErrObjectProtected
	}
	return nil
}

func (s DeletionService) lifecycleProtectionConfiguration(ctx context.Context, b state.ObjectBucket) error {
	lock, ok := s.Provider.(BucketObjectLockProvider)
	versioning, versionOK := s.Provider.(BucketVersioningProvider)
	if !ok || !versionOK {
		return ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return err
	}
	c, err := lock.GetBucketObjectLock(ctx, b.PhysicalName)
	if err != nil {
		return err
	}
	if !c.Valid() || !c.Enabled {
		return ErrUnavailable
	}
	if err = s.before(ctx); err != nil {
		return err
	}
	v, err := versioning.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return err
	}
	if v.Status != "Enabled" || v.MFADelete != "" && v.MFADelete != "Disabled" {
		return ErrUnavailable
	}
	return nil
}

func protectedLifecycleNativeID(j state.ObjectDeletion) string {
	if j.Selector == "null" {
		return "null"
	}
	return j.TargetProviderVersionID
}

// An acknowledgment is insufficient. Every page of the exact-key history must
// be accounted for before clearing custody. A present target must still match
// its frozen discovery identity before any recovery request is permitted.
func (s DeletionService) lifecycleTargetAbsent(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) (bool, error) {
	if j.Lifecycle == nil || j.Lifecycle.ExpectedDeleteMarker == nil || !validNativeVersionID(protectedLifecycleNativeID(j)) || j.Lifecycle.ExpectedProviderVersionID != protectedLifecycleNativeID(j) {
		return false, ErrUnavailable
	}
	versions, err := s.history(ctx, b, j.Key, api.ObjectDeletionHistoryPages)
	if err != nil {
		return false, err
	}
	if _, err = lifecycleHistoryObject(versions, j.Lifecycle.ExpectedProviderVersionID, j.Lifecycle.ExpectedLastModified, time.Now().UTC()); err != nil && !errors.Is(err, ErrLifecycleNotDue) {
		return false, err
	}
	for _, v := range versions {
		if v.ProviderVersionID == protectedLifecycleNativeID(j) {
			if v.DeleteMarker != *j.Lifecycle.ExpectedDeleteMarker || !v.LastModified.Equal(j.Lifecycle.ExpectedLastModified) {
				return false, ErrUnavailable
			}
			return false, nil
		}
	}
	return true, nil
}

func (s DeletionService) recoverProtectedLifecycle(ctx context.Context, b state.ObjectBucket, j state.ObjectDeletion) (state.ObjectDeletion, error) {
	// A null version becomes immutable only after permanent Object Lock and
	// Enabled versioning are freshly confirmed. Ordinary null deletion retains
	// its older uncertainty behavior and never enters this recovery path.
	if j.Selector == "null" {
		if err := s.lifecycleProtectionConfiguration(ctx, b); err != nil {
			return s.deferAttempt(ctx, j, err)
		}
	}
	absent, err := s.lifecycleTargetAbsent(ctx, b, j)
	if err != nil {
		return s.deferAttempt(ctx, j, err)
	}
	if absent {
		j.DeletionVerified = true
		return s.finish(ctx, j, MutableDeleteResult{ProviderVersionID: protectedLifecycleNativeID(j), DeleteMarker: *j.Lifecycle.ExpectedDeleteMarker})
	}
	if err = s.lifecycleProtectionClear(ctx, b, j); err != nil {
		return s.deferAttempt(ctx, j, err)
	}
	if err = s.before(ctx); err != nil {
		return s.deferAttempt(ctx, j, err)
	}
	return s.execute(ctx, b, j, true)
}
