package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ ObjectMultipartPartURLStore = (*MemStore)(nil)

func (m *MemStore) RecordObjectMultipartPartURL(_ context.Context, expected ObjectMultipartUpload, expires time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.objectMultipartUploads[expected.ID]
	if !ok || u.AccountID != expected.AccountID || u.AppID != expected.AppID || u.BucketID != expected.BucketID {
		return ErrNotFound
	}
	now := m.clock()
	if u.State != ObjectMultipartActive || u.PartCount == 0 || !u.ExpiresAt.After(now) || u.Key != expected.Key || u.ProviderUploadID != expected.ProviderUploadID || !expires.After(now) || expires.After(now.Add(time.Duration(api.ObjectMultipartPartURLMaxTTLSeconds)*time.Second)) {
		return ErrConflict
	}
	unsafe := expires.UTC().Add(api.ObjectTransferTimeout + api.ObjectMultipartCleanupGrace)
	if unsafe.After(u.PartURLUnsafeUntil) {
		u.PartURLUnsafeUntil = unsafe
	}
	m.objectMultipartUploads[u.ID] = u
	return nil
}
