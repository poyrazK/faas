package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (r ProjectEnvironmentClonePostgresSnapshotRestore) DeletionOperationIDs() ([]string, error) {
	ids := []string{}
	if r.DeletionOperations != "" && json.Unmarshal([]byte(r.DeletionOperations), &ids) != nil {
		return nil, ErrConflict
	}
	_, ids, err := canonicalCloneForkDeletionOperations(ids)
	return ids, err
}

func canonicalCloneForkDeletionOperations(ids []string) ([]byte, []string, error) {
	copyIDs := append([]string{}, ids...)
	slices.Sort(copyIDs)
	for i, id := range copyIDs {
		if !validCloneSnapshotID(id) || i > 0 && copyIDs[i-1] == id {
			return nil, nil, ErrConflict
		}
	}
	encoded, err := json.Marshal(copyIDs)
	return encoded, copyIDs, err
}

func cloneForkCleanupContextTx(ctx context.Context, tx pgx.Tx, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentCloneOperation, ProjectEnvironmentClonePostgresSnapshot, ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	op, snapshot, err := cloneSnapshotRestoreContextTx(ctx, tx, lease, sourceID, false)
	if err != nil {
		return op, snapshot, ProjectEnvironmentClonePostgresSnapshotRestore{}, err
	}
	if op.Status != CloneOperationCompensating {
		return op, snapshot, ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrConflict
	}
	receipt, err := readCloneSnapshotRestoreTx(ctx, tx, op, snapshot)
	if err == nil && receipt.State != "deleted" && (snapshot.State != "retained" || snapshot.ProviderSnapshotID == "") {
		err = ErrConflict
	}
	return op, snapshot, receipt, err
}

func (s *PgStore) BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, _, receipt, err := cloneForkCleanupContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return receipt, err
	}
	if receipt.State != "deleted" {
		r, err := new(sqlc.Queries).BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, tx, sqlc.BeginProjectEnvironmentClonePostgresSnapshotRestoreCleanupParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, ErrConflict
		}
		if err != nil {
			return receipt, mapErr(err)
		}
		receipt = clonePostgresSnapshotRestoreFromSQL(r)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observed ProjectEnvironmentClonePostgresSnapshotRestoreObservation) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, snapshot, receipt, err := cloneForkCleanupContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return receipt, err
	}
	if receipt.State != "deleting" || receipt.RequestStartedAt.IsZero() {
		return receipt, ErrConflict
	}
	if err := validateCloneSnapshotRestoreObservation(snapshot, receipt, observed); err != nil {
		return receipt, err
	}
	r, err := new(sqlc.Queries).RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentity(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresSnapshotRestoreCleanupIdentityParams{
		OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
		TargetProviderResourceID: observed.TargetProviderResourceID, TargetCreatedAt: pgtype.Timestamptz{Time: observed.TargetCreatedAt, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, ErrConflict
	}
	if err != nil {
		return receipt, mapErr(err)
	}
	return clonePostgresSnapshotRestoreFromSQL(r), mapErr(tx.Commit(ctx))
}

func (s *PgStore) RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, proof ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	return s.mutateCloneForkDeletionProof(ctx, lease, sourceID, proof, false)
}

func (s *PgStore) FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, proof ProjectEnvironmentClonePostgresSnapshotRestoreDeletion) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	return s.mutateCloneForkDeletionProof(ctx, lease, sourceID, proof, true)
}

func (s *PgStore) mutateCloneForkDeletionProof(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, proof ProjectEnvironmentClonePostgresSnapshotRestoreDeletion, finish bool) (ProjectEnvironmentClonePostgresSnapshotRestore, error) {
	if !validCloneLeaseIdentity(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, ErrInvalidArgument
	}
	encoded, ids, err := canonicalCloneForkDeletionOperations(proof.OperationIDs)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresSnapshotRestore{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, _, receipt, err := cloneForkCleanupContextTx(ctx, tx, lease, sourceID)
	if err != nil {
		return receipt, err
	}
	storedIDs, err := receipt.DeletionOperationIDs()
	if err != nil {
		return receipt, err
	}
	if receipt.State != "deleting" && receipt.State != "deleted" || finish && !proof.Done ||
		proof.TargetProviderResourceID != receipt.TargetProviderResourceID ||
		len(storedIDs) > 0 && !slices.Equal(storedIDs, ids) {
		return receipt, ErrConflict
	}
	if receipt.RequestStartedAt.IsZero() {
		if !finish || proof.TargetProviderResourceID != "" || len(ids) > 0 {
			return receipt, ErrConflict
		}
	} else if proof.TargetProviderResourceID == "" || len(ids) == 0 && finish || finish && !slices.Equal(storedIDs, ids) {
		return receipt, ErrConflict
	}
	if receipt.State == "deleted" {
		if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
			return receipt, err
		}
		return receipt, mapErr(tx.Commit(ctx))
	}
	q := new(sqlc.Queries)
	var row sqlc.ProjectEnvironmentClonePostgresSnapshotRestore
	if finish {
		row, err = q.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanup(ctx, tx, sqlc.FinishProjectEnvironmentClonePostgresSnapshotRestoreCleanupParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
			TargetProviderResourceID: proof.TargetProviderResourceID, DeleteOperationIds: encoded})
	} else {
		row, err = q.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperations(ctx, tx, sqlc.RecordProjectEnvironmentClonePostgresSnapshotRestoreDeletionOperationsParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
			TargetProviderResourceID: proof.TargetProviderResourceID, DeleteOperationIds: encoded})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return receipt, ErrConflict
	}
	if err != nil {
		return receipt, mapErr(err)
	}
	return clonePostgresSnapshotRestoreFromSQL(row), mapErr(tx.Commit(ctx))
}
