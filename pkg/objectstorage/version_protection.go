package objectstorage

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var ErrProtectionRejected = errors.New("native protection mutation was rejected")

type VersionProtectionService struct {
	Store         state.ObjectVersionProtectionStore
	References    state.ObjectVersionReferenceStore
	BucketLock    state.ObjectBucketObjectLockStore
	Provider      Provider
	BeforeRequest func(context.Context) error
}

func (s VersionProtectionService) before(ctx context.Context) error {
	if s.BeforeRequest != nil {
		return s.BeforeRequest(ctx)
	}
	return nil
}
func (s VersionProtectionService) nativeLock(ctx context.Context, b state.ObjectBucket) error {
	lock, ok := s.Provider.(BucketObjectLockProvider)
	v, versioned := s.Provider.(BucketVersioningProvider)
	if !ok || !versioned {
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
		return ErrConflict
	}
	if err = s.before(ctx); err != nil {
		return err
	}
	out, err := v.GetBucketVersioning(ctx, b.PhysicalName)
	if err != nil {
		return err
	}
	if out.Status != "Enabled" {
		return ErrConflict
	}
	return nil
}
func (s VersionProtectionService) Read(ctx context.Context, b state.ObjectBucket, key, selector, kind string) (api.ObjectVersionRetention, api.ObjectVersionLegalHold, error) {
	var r api.ObjectVersionRetention
	var h api.ObjectVersionLegalHold
	if !ValidKey(key) || !state.ValidObjectVersionID(selector) || kind != "retention" && kind != "legal_hold" {
		return r, h, ErrInvalid
	}
	if s.References == nil {
		return r, h, ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectVersionProtectionTimeout)
	defer cancel()
	native, err := s.References.ResolveObjectVersion(ctx, b.AccountID, b.ID, key, selector)
	if err != nil {
		return r, h, err
	}
	if !validNativeVersionID(native) || (selector == "null") != (native == "null") {
		return r, h, ErrUnavailable
	}
	if err = s.nativeLock(ctx, b); err != nil {
		return r, h, err
	}
	return s.readNative(ctx, b, key, native, kind)
}
func (s VersionProtectionService) readNative(ctx context.Context, b state.ObjectBucket, key, native, kind string) (api.ObjectVersionRetention, api.ObjectVersionLegalHold, error) {
	var r api.ObjectVersionRetention
	var h api.ObjectVersionLegalHold
	p, ok := s.Provider.(ObjectVersionLockProvider)
	if !ok {
		return r, h, ErrUnsupported
	}
	if err := s.before(ctx); err != nil {
		return r, h, err
	}
	var err error
	if kind == "retention" {
		r, err = p.GetObjectVersionRetention(ctx, b.PhysicalName, key, native)
		if err == nil && !r.Valid() {
			err = ErrUnavailable
		}
	} else {
		h, err = p.GetObjectVersionLegalHold(ctx, b.PhysicalName, key, native)
		if err == nil && !h.Valid() {
			err = ErrUnavailable
		}
	}
	return r.Clone(), h, err
}
func (s VersionProtectionService) Request(ctx context.Context, b state.ObjectBucket, j state.ObjectVersionProtection) (state.ObjectVersionProtection, error) {
	if s.Store == nil || s.BucketLock == nil || !SupportsNativeObjectLock(s.Provider) {
		return j, ErrUnsupported
	}
	u, err := uuid.Parse(j.ID)
	if err != nil || u.String() != j.ID || u.Version() != 4 || u.Variant() != uuid.RFC4122 || !ValidKey(j.Key) || !state.ValidObjectVersionID(j.VersionID) {
		return j, ErrInvalid
	}
	if j.Kind == "retention" && j.Retention != nil && j.LegalHold == nil {
		r := j.Retention.ForWrite()
		if !r.ValidForWrite() {
			return j, ErrInvalid
		}
		if r.EventHold != "" || r.EventHoldDuration != nil {
			return j, ErrUnsupported
		}
		j.Retention = &r
	} else if j.Kind != "legal_hold" || j.LegalHold == nil || j.Retention != nil || !j.LegalHold.Valid() {
		return j, ErrInvalid
	}
	j.AccountID = b.AccountID
	j.AppID = b.AppID
	j.BucketID = b.ID
	// Stable retry IDs inspect accepted intent even while the bucket is fenced.
	if old, e := s.Store.GetObjectVersionProtection(ctx, b.AccountID, b.ID, j.ID); e == nil {
		if old.Key != j.Key || old.VersionID != j.VersionID || old.Kind != j.Kind || !reflect.DeepEqual(old.Retention, j.Retention) || !reflect.DeepEqual(old.LegalHold, j.LegalHold) {
			return old, ErrConflict
		}
		return old, nil
	} else if !errors.Is(e, state.ErrNotFound) {
		return j, e
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectVersionProtectionTimeout)
	defer cancel()
	p := s.Provider.(BucketObjectLockProvider)
	v := s.Provider.(BucketVersioningProvider)
	_, native, err := (BucketObjectLockService{Store: s.BucketLock, Provider: p, Versioning: v, BeforeRequest: s.BeforeRequest}).Read(ctx, b)
	if err != nil {
		return j, err
	}
	if !native.Enabled {
		return j, ErrConflict
	}
	return s.Store.BeginObjectVersionProtection(ctx, j)
}
func (s VersionProtectionService) Reconcile(ctx context.Context, b state.ObjectBucket, id string) (state.ObjectVersionProtection, error) {
	if s.Store == nil {
		return state.ObjectVersionProtection{}, ErrUnsupported
	}
	j, err := s.Store.GetObjectVersionProtection(ctx, b.AccountID, b.ID, id)
	if err != nil || j.State == "ready" {
		return j, err
	}
	if j.State == "failed" {
		if j.LastErrorCode == "provider_rejected" {
			return j, ErrProtectionRejected
		}
		return j, ErrConflict
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObjectVersionProtectionTimeout)
	defer cancel()
	j, err = s.Store.ClaimObjectVersionProtection(ctx, id, uuid.NewString())
	if err != nil {
		return j, err
	}
	if !state.ValidObjectVersionProtectionIntent(j) || j.AccountID != b.AccountID || j.AppID != b.AppID || j.BucketID != b.ID || !validNativeVersionID(j.ProviderVersionID) || (j.VersionID == "null") != (j.ProviderVersionID == "null") {
		return s.deferProtection(ctx, j, ErrUnavailable)
	}
	if err = s.nativeLock(ctx, b); err != nil {
		return s.deferProtection(ctx, j, err)
	}
	r, h, err := s.readNative(ctx, b, j.Key, j.ProviderVersionID, j.Kind)
	if err != nil {
		if !j.Dispatched && errors.Is(err, ErrNotFound) {
			return s.finishProtection(ctx, j, "failed", "preparation_failed", err)
		}
		return s.deferProtection(ctx, j, err)
	}
	if protectionMatches(j, r, h) {
		return s.finishProtection(ctx, j, "ready", "", nil)
	}
	// Once sent, no missing/mismatched policy proves the earlier PUT failed.
	if j.Dispatched {
		return s.deferProtection(ctx, j, ErrUnavailable)
	}
	if j.Kind == "retention" {
		if err = validateRetentionChange(r, *j.Retention, time.Now()); err != nil {
			return s.finishProtection(ctx, j, "failed", "preparation_failed", err)
		}
	}
	if err = s.before(ctx); err != nil {
		return s.deferProtection(ctx, j, err)
	}
	j, err = s.Store.DispatchObjectVersionProtection(ctx, id, j.Token)
	if err != nil {
		return j, err
	}
	p := s.Provider.(ObjectVersionLockProvider)
	if j.Kind == "retention" {
		err = p.PutObjectVersionRetention(ctx, b.PhysicalName, j.Key, j.ProviderVersionID, j.Retention.Clone(), false)
	} else {
		err = p.PutObjectVersionLegalHold(ctx, b.PhysicalName, j.Key, j.ProviderVersionID, *j.LegalHold)
	}
	if errors.Is(err, ErrProtectionRejected) {
		return s.finishProtection(ctx, j, "failed", "provider_rejected", err)
	}
	if err != nil {
		return s.deferProtection(ctx, j, err)
	}
	r, h, err = s.readNative(ctx, b, j.Key, j.ProviderVersionID, j.Kind)
	if err != nil {
		return s.deferProtection(ctx, j, err)
	}
	if !protectionMatches(j, r, h) {
		return s.deferProtection(ctx, j, ErrUnavailable)
	}
	return s.finishProtection(ctx, j, "ready", "", nil)
}
func protectionMatches(j state.ObjectVersionProtection, r api.ObjectVersionRetention, h api.ObjectVersionLegalHold) bool {
	if j.Kind == "legal_hold" {
		return j.LegalHold != nil && *j.LegalHold == h
	}
	return j.Retention != nil && j.Retention.Mode == r.Mode && j.Retention.EventHold == r.EventHold && reflect.DeepEqual(j.Retention.EventHoldDuration, r.EventHoldDuration) && ((j.Retention.RetainUntilDate == nil && r.RetainUntilDate == nil) || (j.Retention.RetainUntilDate != nil && r.RetainUntilDate != nil && j.Retention.RetainUntilDate.Equal(*r.RetainUntilDate)))
}
func validateRetentionChange(old, next api.ObjectVersionRetention, now time.Time) error {
	if old.EventHold != "" || old.EventHoldDuration != nil {
		return ErrUnsupported
	}
	if old.RetainUntilDate != nil && old.RetainUntilDate.After(now) {
		if next.RetainUntilDate == nil || next.RetainUntilDate.Before(*old.RetainUntilDate) || old.Mode == "COMPLIANCE" && next.Mode != "COMPLIANCE" {
			return ErrConflict
		}
	}
	return nil
}
func (s VersionProtectionService) finishProtection(ctx context.Context, j state.ObjectVersionProtection, status, code string, cause error) (state.ObjectVersionProtection, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	out, err := s.Store.FinishObjectVersionProtection(ctx, j.ID, j.Token, status, code)
	if err != nil {
		return out, err
	}
	return out, cause
}
func (s VersionProtectionService) deferProtection(ctx context.Context, j state.ObjectVersionProtection, cause error) (state.ObjectVersionProtection, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.ObjectUploadSettlementTimeout)
	defer cancel()
	code := "provider_uncertain"
	if errors.Is(cause, ErrUnsupported) {
		code = "provider_unsupported"
	}
	if err := s.Store.RetryObjectVersionProtection(ctx, j.ID, j.Token, code); err != nil {
		return j, err
	}
	out, err := s.Store.GetObjectVersionProtection(ctx, j.AccountID, j.BucketID, j.ID)
	if err != nil {
		return j, err
	}
	return out, cause
}
