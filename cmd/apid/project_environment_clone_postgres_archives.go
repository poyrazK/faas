package main

import (
	"context"
	"errors"
	"io"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Storage pins must come from the configured artifact driver, including its
// namespace/root. This private seam is not installed in the public clone path.
type clonePostgresArchiveStorage struct {
	ID, Fingerprint string
	Backend         storage.StorageBackend
}

type clonePostgresArchiveProduce func(context.Context, copyinventory.DatabaseExport, *age.X25519Recipient, io.Writer) (copyarchive.Receipt, error)

// The plan is derived from the original sealed inventory and authenticated
// admission receipts. Its private immutable requirements prevent caller edits
// from substituting selected database metadata. The producer independently owns
// capture placement/SQL authentication; no live source reader is selected here.
func (s *server) projectEnvironmentClonePostgresArchive(ctx context.Context, lease state.ProjectEnvironmentCloneLease, plan copyinventory.ExportPlan, oid uint32,
	artifact clonePostgresArchiveStorage, reserveBytes int64, limits state.ProjectEnvironmentClonePostgresArchiveLimits, produce clonePostgresArchiveProduce) (copyarchive.Receipt, error) {
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresArchiveStore)
	if !ok || mfaIdentities == nil || artifact.Backend == nil {
		return copyarchive.Receipt{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, lease.ExpiresAt)
	defer cancel()
	requirements, err := plan.RequirementsForWorker()
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	var d copyinventory.DatabaseExport
	for _, requirement := range requirements {
		if requirement.Database.OID == oid {
			d = requirement
		}
	}
	if d.Database.OID == 0 {
		return copyarchive.Receipt{}, managedpostgres.ErrConflict
	}
	a, err := store.ProjectEnvironmentClonePostgresArchiveForLease(ctx, lease, d.Scope.SourceDatabaseID, oid)
	if errors.Is(err, state.ErrNotFound) {
		if !d.CapturedAllowConnections || produce == nil || setSecretRecipient == nil {
			return copyarchive.Receipt{}, managedpostgres.ErrUnavailable
		}
		recipient := setSecretRecipient()
		if !cloneInventoryCanOpen(recipient, mfaIdentities()) {
			return copyarchive.Receipt{}, managedpostgres.ErrUnavailable
		}
		a, _, err = store.ReserveProjectEnvironmentClonePostgresArchive(ctx, lease, state.ProjectEnvironmentClonePostgresArchiveRequest{Scope: d.Scope, DatabaseOID: oid,
			InventoryFingerprint: d.InventoryFingerprint, KeyID: recipient.String(), StorageID: artifact.ID, StorageFingerprint: artifact.Fingerprint, ReservedBytes: reserveBytes}, limits)
	}
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	if !a.Scope.Equal(d.Scope) || a.InventoryFingerprint != d.InventoryFingerprint || a.DatabaseOID != oid || a.StorageID != artifact.ID || a.StorageFingerprint != artifact.Fingerprint {
		return copyarchive.Receipt{}, managedpostgres.ErrConflict
	}
	var identities []*age.X25519Identity
	for _, id := range mfaIdentities() {
		if id != nil && id.Recipient().String() == a.KeyID {
			identities = append(identities, id)
		}
	}
	if len(identities) == 0 {
		return copyarchive.Receipt{}, managedpostgres.ErrUnavailable
	}
	backend := clonePostgresArchiveBackend{artifact.Backend}
	var receipt copyarchive.Receipt
	if a.State == "reserved" {
		if produce == nil || !d.CapturedAllowConnections {
			return receipt, managedpostgres.ErrUnavailable
		}
		var dispatch bool
		a, dispatch, err = store.ClaimProjectEnvironmentClonePostgresArchiveUpload(ctx, lease, d.Scope.SourceDatabaseID, oid)
		if err != nil {
			return receipt, err
		}
		if dispatch {
			receipt, err = copyarchive.Upload(ctx, backend, a.StorageKey, d, identities, a.ReservedBytes, func(exportCtx context.Context, w io.Writer) (copyarchive.Receipt, error) {
				return produce(exportCtx, d, identities[0].Recipient(), w)
			})
		} else {
			receipt, err = copyarchive.ReadBack(ctx, backend, a.StorageKey, d, identities, a.ReservedBytes)
		}
	} else {
		receipt, err = copyarchive.ReadBack(ctx, backend, a.StorageKey, d, identities, a.ReservedBytes)
	}
	if errors.Is(err, managedpostgres.ErrNotFound) {
		err = managedpostgres.ErrUnavailable
	}
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	if a.State == "retained" {
		if !copyarchive.SameReceipt(receipt, a.Receipt) {
			return copyarchive.Receipt{}, managedpostgres.ErrConflict
		}
		return receipt, nil
	}
	a, err = store.RecordProjectEnvironmentClonePostgresArchive(ctx, lease, d.Scope.SourceDatabaseID, oid, receipt)
	if err != nil {
		return copyarchive.Receipt{}, err
	}
	if !copyarchive.SameReceipt(receipt, a.Receipt) {
		return copyarchive.Receipt{}, managedpostgres.ErrConflict
	}
	return a.Receipt, nil
}

type clonePostgresArchiveBackend struct{ storage.StorageBackend }

func (b clonePostgresArchiveBackend) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	r, err := b.StorageBackend.Get(ctx, key)
	if storage.IsNotFound(err) {
		err = managedpostgres.ErrNotFound
	}
	return r, err
}
