package main

import (
	"context"
	"crypto/rand"
	"errors"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// The trusted reader must authenticate the exact original provider capture and
// retained reader, and return Capture only after its SQL/provider postchecks.
// Recovery never invokes this seam. Public clone wiring remains gated.
type clonePostgresContentsRead func(context.Context, copyinventory.DatabaseExport, [32]byte) (copycontents.Manifest, error)

// Recover original ciphertext/recipient before considering a source read. The
// export plan must come from the retained inventory; a current SQL catalogue is
// never used to repair an unreadable manifest. This grants no stage readiness.
func (s *server) projectEnvironmentClonePostgresContents(ctx context.Context, l state.ProjectEnvironmentCloneLease, plan copyinventory.ExportPlan, oid uint32,
	reserveBytes int64, limits state.ProjectEnvironmentClonePostgresContentsLimits, read clonePostgresContentsRead) (copycontents.Manifest, error) {
	store, ok := s.store.(state.ProjectEnvironmentClonePostgresContentsStore)
	if !ok || mfaIdentities == nil {
		return copycontents.Manifest{}, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	requirements, err := plan.RequirementsForWorker()
	if err != nil {
		return copycontents.Manifest{}, err
	}
	var d copyinventory.DatabaseExport
	for _, r := range requirements {
		if r.Database.OID == oid {
			d = r
		}
	}
	if d.Database.OID == 0 || d.Scope.OperationID != l.Operation.ID || d.Scope.AccountID != l.Operation.AccountID || d.Scope.ProjectID != l.Operation.ProjectID {
		return copycontents.Manifest{}, managedpostgres.ErrConflict
	}
	a, err := store.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, d.Scope.SourceDatabaseID, oid)
	identities := mfaIdentities()
	if err == nil && a.State == "captured" {
		return copycontents.Open(identities, d, a.Sealed)
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return copycontents.Manifest{}, err
	}
	if l.Operation.Status != state.CloneOperationCapturing || !d.CapturedAllowConnections || read == nil {
		return copycontents.Manifest{}, managedpostgres.ErrUnavailable
	}
	if errors.Is(err, state.ErrNotFound) {
		if setSecretRecipient == nil {
			return copycontents.Manifest{}, managedpostgres.ErrUnavailable
		}
		recipient := setSecretRecipient()
		if !cloneInventoryCanOpen(recipient, identities) {
			return copycontents.Manifest{}, managedpostgres.ErrUnavailable
		}
		a, _, err = store.ReserveProjectEnvironmentClonePostgresContents(ctx, l, state.ProjectEnvironmentClonePostgresContentsRequest{
			Scope: d.Scope, DatabaseOID: oid, InventoryFingerprint: d.InventoryFingerprint, KeyID: recipient.String(), ReservedBytes: reserveBytes}, limits)
		if err != nil {
			return copycontents.Manifest{}, err
		}
	}
	if !a.Scope.Equal(d.Scope) || a.DatabaseOID != oid || a.InventoryFingerprint != d.InventoryFingerprint {
		return copycontents.Manifest{}, managedpostgres.ErrConflict
	}
	if a.State == "captured" {
		return copycontents.Open(identities, d, a.Sealed)
	}
	// An existing undispatched reservation retains its original recipient even
	// when the configured current recipient changes. Missing old keys fail closed.
	var recipient *age.X25519Recipient
	for _, id := range identities {
		if id != nil && id.Recipient().String() == a.KeyID {
			recipient = id.Recipient()
			break
		}
	}
	if recipient == nil {
		return copycontents.Manifest{}, managedpostgres.ErrUnavailable
	}
	var key [32]byte
	if _, err = rand.Read(key[:]); err != nil {
		return copycontents.Manifest{}, managedpostgres.ErrUnavailable
	}
	if err = ctx.Err(); err != nil {
		return copycontents.Manifest{}, err
	}
	manifest, err := read(ctx, d, key)
	if err != nil {
		return copycontents.Manifest{}, err
	}
	sealed, err := copycontents.Seal(recipient, manifest)
	if err != nil {
		return copycontents.Manifest{}, err
	}
	// Check the complete original private source descriptor before storage. The
	// metadata-only ledger cannot decrypt/verify the independently captured data.
	if _, err = copycontents.Open(identities, d, sealed); err != nil {
		return copycontents.Manifest{}, err
	}
	a, err = store.RecordProjectEnvironmentClonePostgresContents(ctx, l, d.Scope.SourceDatabaseID, oid, sealed)
	if err != nil {
		return copycontents.Manifest{}, err
	}
	return copycontents.Open(identities, d, a.Sealed)
}
