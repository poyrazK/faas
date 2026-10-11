package main

import (
	"context"
	"crypto/rand"
	"errors"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// The provider implementation must independently authenticate the exact owned
// capture/compute before calling copyinventory.Read. The scope is durable input,
// not SQL connection authority. No live provider reader is installed yet.
type clonePostgresInventoryRead func(context.Context, copyinventory.Scope, [32]byte) (copyinventory.Config, copyinventory.Inventory, error)

// Recover committed metadata first. A failed open never falls back to reading a
// newer catalogue or generating a replacement key. This private helper grants
// no source release, target readiness or public complete-clone admission.
func (s *server) projectEnvironmentClonePostgresInventory(ctx context.Context, lease state.ProjectEnvironmentCloneLease, sourceID string, read clonePostgresInventoryRead) (copyinventory.Inventory, error) {
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresInventoryStore)
	if !ok || mfaIdentities == nil {
		return copyinventory.Inventory{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	scope, err := store.ProjectEnvironmentClonePostgresInventoryScopeForLease(ctx, lease, sourceID)
	if err != nil {
		return copyinventory.Inventory{}, err
	}
	identities := mfaIdentities()
	receipt, err := store.ProjectEnvironmentClonePostgresInventoryForLease(ctx, lease, sourceID)
	if err == nil {
		return copyinventory.OpenInventory(identities, scope, receipt.Sealed)
	}
	if !errors.Is(err, state.ErrNotFound) {
		return copyinventory.Inventory{}, err
	}
	if lease.Operation.Status != state.CloneOperationCapturing || read == nil || setSecretRecipient == nil {
		return copyinventory.Inventory{}, managedpostgres.ErrUnavailable
	}
	recipient := setSecretRecipient()
	if !cloneInventoryCanOpen(recipient, identities) {
		return copyinventory.Inventory{}, managedpostgres.ErrUnavailable
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return copyinventory.Inventory{}, managedpostgres.ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return copyinventory.Inventory{}, err
	}
	cfg, inventory, err := read(ctx, scope, key)
	if err != nil {
		return copyinventory.Inventory{}, err
	}
	if cfg.FingerprintKey != key {
		return copyinventory.Inventory{}, managedpostgres.ErrConflict
	}
	sealed, err := copyinventory.SealInventory(recipient, scope, cfg, inventory)
	if err != nil {
		return copyinventory.Inventory{}, err
	}
	receipt, _, err = store.RecordProjectEnvironmentClonePostgresInventory(ctx, lease, sourceID, sealed)
	if err != nil {
		return copyinventory.Inventory{}, err
	}
	return copyinventory.OpenInventory(identities, scope, receipt.Sealed)
}

func cloneInventoryCanOpen(recipient *age.X25519Recipient, identities []*age.X25519Identity) bool {
	if recipient == nil {
		return false
	}
	for _, identity := range identities {
		if identity != nil && identity.Recipient().String() == recipient.String() {
			return true
		}
	}
	return false
}
