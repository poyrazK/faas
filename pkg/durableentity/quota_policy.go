package durableentity

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// RaiseQuotaEntityLimit is private platform authority used only while a managed
// quota migration holds admission. It changes no application state or receipts.
// Acquisition fences obsolete owners; retries never lower an already raised cap.
func (m *Manager) RaiseQuotaEntityLimit(ctx context.Context, id ID, limit int64) error {
	if limit <= 0 || limit != m.storageLimit {
		return ErrUnsupported
	}
	before, etag, err := m.readManifest(ctx, id)
	if err != nil {
		return err
	}
	if before.Retirement != nil {
		return ErrRetired
	}
	if before.StorageLimitBytes == limit {
		return nil
	}
	if before.StorageLimitBytes > limit {
		return ErrConflict
	}
	now := m.now().UTC()
	if before.OwnerID != "" && now.Before(before.ExpiresAt) {
		return ErrBusy
	}
	if before.Epoch == ^uint64(0) {
		return ErrLimit
	}
	before.Epoch++
	before.OwnerID, before.Token, before.ExpiresAt = uuid.NewString(), uuid.NewString(), now.Add(m.lease)
	before.StorageLimitBytes = limit
	// A single CAS acquires ownership and raises the cap. Unlike ordinary
	// acquisition it never tightens a cap raised by a concurrent newer policy.
	if err := m.putManifest(ctx, before, etag); err != nil {
		return err
	}
	claim := claimFrom(before)
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
	defer cancel()
	return m.Release(releaseCtx, claim)
}
