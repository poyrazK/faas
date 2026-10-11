package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) mutateClonePostgresCopyTargetCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID, action string, proof ProjectEnvironmentClonePostgresCopyTargetDeletion) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresCopyTarget{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, capture, source, resource, point, err := clonePostgresCopyContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresCopyTarget{}, err
	}
	if op.Status != CloneOperationCompensating {
		return ProjectEnvironmentClonePostgresCopyTarget{}, ErrConflict
	}
	r, err := readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
	if err != nil {
		return r, err
	}
	q := new(sqlc.Queries)
	if r.State == "retired" {
		if action != "begin" && (r.RequestStartedAt.IsZero() || !proof.Done || proof.ProviderResourceID != r.ProviderResourceID || !proof.CreatedAt.Equal(r.ProviderCreatedAt)) {
			return r, ErrConflict
		}
	} else {
		var row sqlc.ProjectEnvironmentClonePostgresCopyTarget
		switch action {
		case "begin":
			if r.State == "reserved" || r.RequestStartedAt.IsZero() {
				return r, ErrConflict
			}
			row, err = q.BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, tx, sqlc.BeginProjectEnvironmentClonePostgresCopyTargetCleanupParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		case "identity", "finish":
			if r.State != "deleting" || r.RequestStartedAt.IsZero() || !validCloneSnapshotID(proof.ProviderResourceID) || proof.ProviderResourceID == source.ProviderResourceID || proof.ProviderResourceID == source.DataResourceID || proof.ProviderResourceID == capture.TargetProviderResourceID ||
				proof.CreatedAt.Before(capture.TargetCreatedAt) || proof.CreatedAt.Nanosecond()%1000 != 0 || r.ProviderResourceID != "" && (r.ProviderResourceID != proof.ProviderResourceID || !r.ProviderCreatedAt.Equal(proof.CreatedAt)) {
				return r, ErrConflict
			}
			if action == "identity" {
				_, err = q.PinProjectEnvironmentClonePostgresCopyTargetCleanupDatabase(ctx, tx, sqlc.PinProjectEnvironmentClonePostgresCopyTargetCleanupDatabaseParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderResourceID: proof.ProviderResourceID, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
				if err == nil {
					row, err = q.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentityParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderResourceID: proof.ProviderResourceID, ProviderCreatedAt: pgtype.Timestamptz{Time: proof.CreatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
				}
			} else {
				if !proof.Done || r.ProviderResourceID == "" {
					return r, ErrConflict
				}
				_, err = q.RetireProjectEnvironmentClonePostgresCopyTargetDatabase(ctx, tx, sqlc.RetireProjectEnvironmentClonePostgresCopyTargetDatabaseParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
				if err == nil {
					row, err = q.FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresCopyTargetCleanupParams{OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ProviderResourceID: proof.ProviderResourceID, ProviderCreatedAt: pgtype.Timestamptz{Time: proof.CreatedAt, Valid: true}, ExpectedRevision: op.Revision, WorkerToken: lease.Token})
				}
			}
		default:
			return r, ErrInvalidArgument
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return r, ErrConflict
		}
		if err != nil {
			return r, mapErr(err)
		}
		if row.OperationID.Valid {
			r, err = readClonePostgresCopyTargetTx(ctx, tx, op, capture, source, resource, point)
			if err != nil {
				return r, err
			}
		}
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return r, err
	}
	return r, mapErr(tx.Commit(ctx))
}

func (s *PgStore) BeginProjectEnvironmentClonePostgresCopyTargetCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	return s.mutateClonePostgresCopyTargetCleanup(ctx, lease, sourceID, "begin", ProjectEnvironmentClonePostgresCopyTargetDeletion{})
}
func (s *PgStore) RecordProjectEnvironmentClonePostgresCopyTargetCleanupIdentity(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, proof ProjectEnvironmentClonePostgresCopyTargetDeletion) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	return s.mutateClonePostgresCopyTargetCleanup(ctx, lease, sourceID, "identity", proof)
}
func (s *PgStore) FinishProjectEnvironmentClonePostgresCopyTargetCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, proof ProjectEnvironmentClonePostgresCopyTargetDeletion) (ProjectEnvironmentClonePostgresCopyTarget, error) {
	return s.mutateClonePostgresCopyTargetCleanup(ctx, lease, sourceID, "finish", proof)
}
