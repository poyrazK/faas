package durableentity

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Acquire creates or takes over an expired/released entity. Even the same
// OwnerID gets a new epoch and token after release or expiry. On ErrUncertain,
// the returned claim can be confirmed by Renew; it is not yet usable authority.
func (m *Manager) Acquire(ctx context.Context, id ID, ownerID string) (Claim, error) {
	if !id.valid() || !validIdentity(ownerID) {
		return Claim{}, ErrInvalid
	}
	value, etag, err := m.readManifest(ctx, id)
	if errors.Is(err, ErrNotFound) {
		value = manifest{Schema: 1, ID: id}
	} else if err != nil {
		return Claim{}, err
	}
	now := m.now().UTC()
	if value.OwnerID != "" && now.Before(value.ExpiresAt) {
		return Claim{}, ErrBusy
	}
	if value.Epoch == ^uint64(0) {
		return Claim{}, ErrLimit
	}
	value.Epoch++
	value.StorageLimitBytes = stricterStorageLimit(value.StorageLimitBytes, m.storageLimit)
	if value.Version == 0 {
		value.StorageUsage = &StorageUsage{}
	}
	value.OwnerID, value.Token, value.ExpiresAt = ownerID, uuid.NewString(), now.Add(m.lease)
	claim := claimFrom(value)
	return claim, m.putManifest(ctx, value, etag)
}

func claimFrom(value manifest) Claim {
	return Claim{ID: value.ID, OwnerID: value.OwnerID, Token: value.Token, Epoch: value.Epoch, ExpiresAt: value.ExpiresAt}
}

func (m *Manager) owned(ctx context.Context, claim Claim) (manifest, string, error) {
	value, etag, err := m.readManifest(ctx, claim.ID)
	if err != nil {
		return manifest{}, "", err
	}
	if claim.Token == "" || value.Token != claim.Token || value.OwnerID != claim.OwnerID || value.Epoch != claim.Epoch || !m.now().Before(value.ExpiresAt) {
		return manifest{}, "", ErrStaleOwner
	}
	return value, etag, nil
}

// Renew reloads current state, so renewing a claim never overwrites a commit.
// A concurrent transition/renewal can return ErrConflict; reload before retry.
func (m *Manager) Renew(ctx context.Context, claim Claim) (Claim, error) {
	value, etag, err := m.owned(ctx, claim)
	if err != nil {
		return Claim{}, err
	}
	value.ExpiresAt = m.now().UTC().Add(m.lease)
	return claimFrom(value), m.putManifest(ctx, value, etag)
}

// Release keeps the committed snapshot and epoch but removes live authority.
// Unknown outcomes require read/recovery; no unconditional cleanup is safe.
func (m *Manager) Release(ctx context.Context, claim Claim) error {
	value, etag, err := m.owned(ctx, claim)
	if err != nil {
		return err
	}
	value.OwnerID, value.Token, value.ExpiresAt = "", "", time.Time{}
	return m.putManifest(ctx, value, etag)
}
