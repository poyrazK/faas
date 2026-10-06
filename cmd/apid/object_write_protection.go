package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) resolveObjectWriteProtection(ctx context.Context, b state.ObjectBucket, requested *api.ObjectWriteProtection) (state.ObjectWriteProtectionSnapshot, error) {
	var p state.ObjectWriteProtectionSnapshot
	if requested != nil {
		p.Requested = requested.ForWrite()
		if p.Requested.Empty() || !p.Requested.Valid() {
			return p, objectstorage.ErrInvalid
		}
	}
	backend, err := s.objectStorage.Resolve(b.BackendID, b.BackendFingerprint)
	if err != nil {
		return p, err
	}
	p.AdmitEventHolds = backend.ObjectLock.EventHolds
	protected := !p.Requested.Empty()
	event := p.Requested.Retention != nil && p.Requested.Retention.EventHold != ""
	if store, ok := s.store.(state.ObjectBucketObjectLockStore); ok {
		j, e := store.GetObjectBucketObjectLock(ctx, b.AccountID, b.AppID, b.ID)
		if e != nil {
			return p, e
		}
		protected = protected || j.EnabledRequired || j.NativeEnabledObserved
		event = event || p.Requested.Retention == nil && j.ObservedConfiguration != nil && j.ObservedConfiguration.DefaultRetention != nil && j.ObservedConfiguration.DefaultRetention.DefaultEventHold != nil
	}
	if event && !backend.ObjectLock.EventHolds {
		return p, objectstorage.ErrUnsupported
	}
	if protected {
		if !backend.ObjectLock.Enabled {
			return p, objectstorage.ErrUnsupported
		}
		if _, ok := backend.Provider.(objectstorage.ObjectWriteProtectionProvider); !ok {
			return p, objectstorage.ErrUnsupported
		}
	}
	return p, nil
}
