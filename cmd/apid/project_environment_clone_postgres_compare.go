package main

import (
	"context"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copycontents"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copydatabases"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
)

// The native window supplies the ordinal, owner and SQL opening. Child rollback
// and complete provider postchecks precede persistence of the first sealed match.
func (s *server) projectEnvironmentClonePostgresCompare(ctx context.Context, source capturedProjectEnvironmentDatabasePlan, bootstrap copyarchive.RestoreTarget,
	request managedpostgres.SnapshotCopyTargetDatabaseSQLRequest, manifest copycontents.Manifest, actual copyarchive.RestoreTarget, owner, imported uuid.UUID, attempt int32,
	recipient *age.X25519Recipient, identities []*age.X25519Identity, cfg copycontents.Config, authorize copyroles.Authorize, access copydatabases.VerificationTarget,
	persist func(context.Context, copycontents.SealedMatch) (copycontents.SealedMatch, error)) (copycontents.RetainedMatch, error) {
	var zero copycontents.RetainedMatch
	target, err := access.TargetForWorker()
	binding, bindingErr := access.IdentityForWorker()
	if err != nil || bindingErr != nil || target != actual || binding.OwnerID != owner || binding.ImportID != imported || binding.Attempt != attempt {
		return zero, managedpostgres.ErrConflict
	}
	var matched copycontents.Match
	err = s.managedPostgres.WithSnapshotCopyTargetDatabaseSQL(ctx, clonePostgresSnapshotDefinition(source), request,
		func(ctx context.Context, selected *pgx.Conn, _ managedpostgres.SnapshotCopyTargetSQLIdentity) error {
			placement := func(ctx context.Context, got *pgx.Conn, target copyarchive.RestoreTarget) error {
				if got != selected || target != actual {
					return managedpostgres.ErrConflict
				}
				if err := authorize(ctx, bootstrap); err != nil {
					return err
				}
				observed, err := s.managedPostgres.FindSnapshotCopyTarget(ctx, clonePostgresSnapshotDefinition(source), request.Preparation)
				if err == nil && !observed.Prepared {
					err = managedpostgres.ErrUnavailable
				}
				if err == nil {
					err = authorize(ctx, bootstrap)
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
		return zero, err
	}
	sealed, err := copycontents.SealMatch(recipient, manifest, actual, owner, imported, binding.OpenedAt, matched)
	if err != nil {
		return zero, err
	}
	retained, err := persist(ctx, sealed)
	if err != nil {
		return zero, err
	}
	return copycontents.OpenMatch(identities, manifest, actual, owner, imported, retained)
}
