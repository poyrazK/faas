package main

import (
	"context"
	"errors"
	"reflect"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/state"
)

// Compare actual restored data against the original retained contents. Persist
// its sealed match while access remains open, then publish only after exact native
// closure and provider postchecks. Recovery is close-only, never another restore
// or current-source read. This private proof does not grant stage readiness.
func (s *server) projectEnvironmentClonePostgresVerification(ctx context.Context, l state.ProjectEnvironmentCloneLease,
	source capturedProjectEnvironmentDatabasePlan, exports copyinventory.ExportPlan, oid uint32, cfg copycontents.Config) (state.ProjectEnvironmentClonePostgresVerification, error) {
	var zero state.ProjectEnvironmentClonePostgresVerification
	verifications, verificationOK := s.store.(state.ProjectEnvironmentClonePostgresVerificationStore)
	contents, contentsOK := s.store.(state.ProjectEnvironmentClonePostgresContentsStore)
	imports, importOK := s.store.(state.ProjectEnvironmentClonePostgresImportStore)
	pins, pinsOK := s.store.(state.ProjectEnvironmentClonePostgresDatabaseSQLPinsStore)
	if !verificationOK || !contentsOK || !importOK || !pinsOK || mfaIdentities == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	ctx, cancel := context.WithDeadline(ctx, l.ExpiresAt)
	defer cancel()
	// A nil reader deliberately makes a missing/unreadable source manifest fail.
	// Verification has no source recapture or new preparation authority.
	manifest, err := s.projectEnvironmentClonePostgresContents(ctx, l, exports, oid, 0, state.ProjectEnvironmentClonePostgresContentsLimits{}, nil)
	if err != nil {
		return zero, err
	}
	prepared, err := s.openProjectEnvironmentClonePostgresDatabasePreparation(ctx, l, source, exports, oid)
	if err != nil {
		return zero, err
	}
	actual, err := prepared.receipt.TargetForWorker()
	if err != nil {
		return zero, err
	}
	id := actual.Scope.SourceDatabaseID
	captured, err := contents.ProjectEnvironmentClonePostgresContentsForLease(ctx, l, id, oid)
	if err != nil {
		return zero, err
	}
	imported, err := imports.ProjectEnvironmentClonePostgresImportForLease(ctx, l, id, oid)
	if err != nil {
		return zero, err
	}
	if !clonePostgresInventoryMatchesSource(l, source, actual.Scope) || captured.State != "captured" || manifest.Fingerprint() != captured.Sealed.Fingerprint ||
		imported.State == "reserved" || !imported.MatchesDatabaseSQLPins(prepared.owner) || !imported.MatchesTarget(actual) || !captured.Scope.Equal(actual.Scope) {
		return zero, managedpostgres.ErrConflict
	}
	owner, err := verifications.ProjectEnvironmentClonePostgresVerificationForLease(ctx, l, id, oid)
	identities := mfaIdentities()
	if errors.Is(err, state.ErrNotFound) {
		if l.Operation.Status != state.CloneOperationCapturing || setSecretRecipient == nil {
			return zero, managedpostgres.ErrUnavailable
		}
		recipient := setSecretRecipient()
		if !cloneInventoryCanOpen(recipient, identities) {
			return zero, managedpostgres.ErrUnavailable
		}
		owner, _, err = verifications.ReserveProjectEnvironmentClonePostgresVerification(ctx, l, state.ProjectEnvironmentClonePostgresVerificationRequest{
			Scope: actual.Scope, DatabaseOID: oid, ContentsCiphertextSHA256: captured.Sealed.CiphertextSHA256, DatabaseSQLPinsCiphertextSHA256: prepared.owner.Sealed.CiphertextSHA256,
			ImportID: imported.ImportID, TargetFingerprint: imported.TargetFingerprint, KeyID: recipient.String(), ReservedBytes: api.PostgresCopyVerificationCiphertextMaxBytes})
	}
	if err != nil {
		return zero, err
	}
	if s.managedPostgres == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	var recipient *age.X25519Recipient
	for _, id := range identities {
		if id != nil && id.Recipient().String() == owner.KeyID {
			recipient = id.Recipient()
			break
		}
	}
	if recipient == nil {
		return zero, managedpostgres.ErrUnavailable
	}
	verificationID, err := uuid.Parse(owner.VerificationID)
	importID, importErr := uuid.Parse(imported.ImportID)
	if err != nil || importErr != nil {
		return zero, managedpostgres.ErrConflict
	}
	if owner.State == "reserved" {
		owner, _, err = verifications.ClaimProjectEnvironmentClonePostgresVerification(ctx, l, id, oid)
		if err != nil {
			return zero, err
		}
	}
	authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
		fresh, err := verifications.ProjectEnvironmentClonePostgresVerificationForLease(ctx, l, id, oid)
		if err != nil {
			return err
		}
		child, err := pins.ProjectEnvironmentClonePostgresDatabaseSQLPinsForLease(ctx, l, id, oid)
		if err == nil && (target != prepared.bootstrap || !reflect.DeepEqual(fresh, owner) || !sameClonePostgresDatabaseSQLPins(child, prepared.owner)) {
			err = managedpostgres.ErrConflict
		}
		return err
	}
	bootstrapRequest, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, actual.Scope, prepared.bootstrap)
	if err != nil {
		return zero, err
	}
	childRequest, err := s.projectEnvironmentClonePostgresTargetDatabaseSQLRequest(ctx, l, source, actual.Scope, actual)
	if err != nil {
		return zero, err
	}
	var retained copycontents.RetainedMatch
	var closure copydatabases.VerificationClosure
	if owner.State == "compared" || owner.State == "verified" {
		retained, err = copycontents.OpenMatch(identities, manifest, actual, verificationID, importID, owner.Sealed)
		if err != nil {
			return zero, err
		}
		err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), bootstrapRequest,
			func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
				var err error
				closure, err = prepared.receipt.CloseVerificationAccess(ctx, conn, exports, importID, verificationID, authorize)
				return err
			})
	} else if owner.State == "verifying" {
		err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), bootstrapRequest,
			func(ctx context.Context, conn *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
				var err error
				closure, err = prepared.receipt.WithVerificationAccess(ctx, conn, exports, importID, verificationID, authorize,
					func(ctx context.Context, access copydatabases.VerificationTarget) error {
						target, err := access.TargetForWorker()
						binding, bindingErr := access.IdentityForWorker()
						if err != nil || bindingErr != nil || target != actual || binding.OwnerID != verificationID || binding.ImportID != importID {
							return managedpostgres.ErrConflict
						}
						var matched copycontents.Match
						err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), childRequest,
							func(ctx context.Context, selected *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
								placement := func(ctx context.Context, got *pgx.Conn, target copyarchive.RestoreTarget) error {
									if got != selected || target != actual {
										return managedpostgres.ErrConflict
									}
									if err := authorize(ctx, prepared.bootstrap); err != nil {
										return err
									}
									observed, err := s.managedPostgres.FindSnapshotCopyTarget(ctx, clonePostgresSnapshotDefinition(source), childRequest.Preparation)
									if err == nil && !observed.Prepared {
										err = managedpostgres.ErrUnavailable
									}
									if err == nil {
										err = authorize(ctx, prepared.bootstrap)
									}
									return err
								}
								return access.WithReadOnly(ctx, selected, placement, func(ctx context.Context, tx pgx.Tx) error {
									var err error
									matched, err = manifest.CompareTarget(ctx, tx, target, cfg, placement)
									return err
								})
							})
						if err != nil {
							return err
						}
						sealed, err := copycontents.SealMatch(recipient, manifest, actual, verificationID, importID, binding.OpenedAt, matched)
						if err != nil {
							return err
						}
						owner, err = verifications.RecordProjectEnvironmentClonePostgresVerificationMatch(ctx, l, id, oid, sealed)
						if err != nil {
							return err
						}
						retained, err = copycontents.OpenMatch(identities, manifest, actual, verificationID, importID, owner.Sealed)
						return err
					})
				return err
			})
	} else {
		return zero, managedpostgres.ErrConflict
	}
	if err != nil {
		return zero, err
	}
	if err = authorize(ctx, prepared.bootstrap); err != nil {
		return zero, err
	}
	verified, err := verifications.RecordProjectEnvironmentClonePostgresVerificationClosure(ctx, l, id, oid,
		state.ProjectEnvironmentClonePostgresVerificationCompletion{Manifest: manifest, Match: retained, Target: actual, Preparation: prepared.receipt, Closure: closure})
	if err != nil {
		return zero, err
	}
	return verified, nil
}
