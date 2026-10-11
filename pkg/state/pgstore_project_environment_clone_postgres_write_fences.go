package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentClonePostgresWriteFenceStore = (*PgStore)(nil)

func clonePostgresWriteFenceFromSQL(row sqlc.ProjectEnvironmentClonePostgresWriteFence) ProjectEnvironmentClonePostgresWriteFence {
	return ProjectEnvironmentClonePostgresWriteFence{OperationID: pgUUIDString(row.OperationID), SourceDatabaseID: pgUUIDString(row.SourceDatabaseID),
		SourceVersion: row.SourceVersion, BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint,
		SourceProviderResourceID: row.SourceProviderResourceID, SourceDataResourceID: row.SourceDataResourceID, State: row.State,
		RemoteTerminalState: row.RemoteTerminalState.String, RemoteReleasedAt: row.RemoteReleasedAt.Time, ReleasedAt: row.ReleasedAt.Time, CreatedAt: row.CreatedAt.Time}
}

func validClonePostgresFenceLease(lease ProjectEnvironmentCloneLease) bool {
	return validCloneLeaseIdentity(lease) && validCloneCredentialSourceID(lease.Operation.ID) &&
		validCloneCredentialSourceID(lease.Operation.AccountID) && validCloneCredentialSourceID(lease.Operation.ProjectID)
}

// Source placement is rechecked under the lifecycle deletion row lock.
func lockClonePostgresFenceSource(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, receipt ProjectEnvironmentClonePostgresWriteFence) error {
	live, err := sqlc.New().ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{
		AccountID: mustPgUUID(op.AccountID), ID: mustPgUUID(receipt.SourceDatabaseID)})
	if err != nil {
		return mapErr(err)
	}
	if live.State != "ready" || live.DeletedAt.Valid || live.BackendID != receipt.BackendID || live.BackendFingerprint != receipt.BackendFingerprint ||
		live.ProviderResourceID.String != receipt.SourceProviderResourceID || live.DataResourceID.String != receipt.SourceDataResourceID {
		return ErrConflict
	}
	return nil
}

func readClonePostgresWriteFence(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, sourceID string) (ProjectEnvironmentClonePostgresWriteFence, error) {
	row, err := sqlc.New().ReadClonePostgresWriteFence(ctx, tx, sqlc.ReadClonePostgresWriteFenceParams{
		OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID)})
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, mapErr(err)
	}
	return clonePostgresWriteFenceFromSQL(row), nil
}

func (s *PgStore) ReserveProjectEnvironmentClonePostgresWriteFence(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresWriteFence, error) {
	if !validClonePostgresFenceLease(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresWriteFence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentClonePostgresWriteFence{}, ErrConflict
	}
	records, err := cloneWorkloadRecordsDB(ctx, tx, op.AccountID, op.ProjectID, op.ID)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	views, err := cloneBindingViews(records)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	source, err := capturedCloneWriteFenceDatabase(views, sourceID)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	hash, err := ProjectEnvironmentCloneDatabaseSourceHash(source)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	want := ProjectEnvironmentClonePostgresWriteFence{OperationID: op.ID, SourceDatabaseID: sourceID, SourceVersion: hash,
		BackendID: source.BackendID, BackendFingerprint: source.BackendFingerprint,
		SourceProviderResourceID: source.ProviderResourceID, SourceDataResourceID: source.DataResourceID}
	// Lifecycle deletion takes this same row lock before checking the hold.
	if err := lockClonePostgresFenceSource(ctx, tx, op, want); err != nil {
		return want, err
	}
	receipt, err := readClonePostgresWriteFence(ctx, tx, op, sourceID)
	if errors.Is(err, ErrNotFound) {
		row, insertErr := sqlc.New().InsertClonePostgresWriteFence(ctx, tx, sqlc.InsertClonePostgresWriteFenceParams{
			OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), SourceVersion: hash,
			BackendID: want.BackendID, BackendFingerprint: want.BackendFingerprint,
			SourceProviderResourceID: want.SourceProviderResourceID, SourceDataResourceID: want.SourceDataResourceID,
			ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		if errors.Is(insertErr, pgx.ErrNoRows) {
			return receipt, ErrConflict
		}
		if insertErr != nil {
			return receipt, mapErr(insertErr)
		}
		receipt, err = clonePostgresWriteFenceFromSQL(row), nil
	}
	if err != nil {
		return receipt, err
	}
	if receipt.State != "held" || receipt.SourceVersion != want.SourceVersion || receipt.BackendID != want.BackendID ||
		receipt.BackendFingerprint != want.BackendFingerprint || receipt.SourceProviderResourceID != want.SourceProviderResourceID || receipt.SourceDataResourceID != want.SourceDataResourceID {
		return receipt, ErrConflict
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ProjectEnvironmentClonePostgresWriteFencesForLease(ctx context.Context, lease ProjectEnvironmentCloneLease) ([]ProjectEnvironmentClonePostgresWriteFence, error) {
	if !validClonePostgresFenceLease(lease) {
		return nil, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListClonePostgresWriteFences(ctx, tx, mustPgUUID(op.ID))
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ProjectEnvironmentClonePostgresWriteFence, 0, len(rows))
	for _, row := range rows {
		receipt := clonePostgresWriteFenceFromSQL(row)
		if receipt.State != "released" {
			if err := lockClonePostgresFenceSource(ctx, tx, op, receipt); err != nil {
				return nil, err
			}
		}
		out = append(out, receipt)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return nil, err
	}
	return out, mapErr(tx.Commit(ctx))
}

func (s *PgStore) BeginProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string) (ProjectEnvironmentClonePostgresWriteFence, error) {
	return s.mutateClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, nil)
}

func (s *PgStore) FinishProjectEnvironmentClonePostgresWriteFenceAbandonment(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observation ProjectEnvironmentClonePostgresFenceAbandonment) (ProjectEnvironmentClonePostgresWriteFence, error) {
	return s.mutateClonePostgresWriteFenceAbandonment(ctx, lease, sourceID, &observation)
}

func (s *PgStore) mutateClonePostgresWriteFenceAbandonment(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceID string, observation *ProjectEnvironmentClonePostgresFenceAbandonment) (ProjectEnvironmentClonePostgresWriteFence, error) {
	if !validClonePostgresFenceLease(lease) || !validCloneCredentialSourceID(sourceID) {
		return ProjectEnvironmentClonePostgresWriteFence{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := cloneWriteFenceLeaseTx(ctx, tx, lease)
	if err != nil {
		return ProjectEnvironmentClonePostgresWriteFence{}, err
	}
	if op.Status != CloneOperationCompensating {
		return ProjectEnvironmentClonePostgresWriteFence{}, ErrConflict
	}
	receipt, err := readClonePostgresWriteFence(ctx, tx, op, sourceID)
	if err != nil {
		return receipt, err
	}
	if observation != nil {
		if observation.OwnerToken != op.ID || observation.SourceDataResourceID != receipt.SourceDataResourceID ||
			(observation.State != "released" && observation.State != "abandoned") || observation.ReleasedAt.IsZero() || observation.ReleasedAt.Nanosecond()%1000 != 0 ||
			receipt.State == "released" && (receipt.RemoteTerminalState != observation.State || !receipt.RemoteReleasedAt.Equal(observation.ReleasedAt)) {
			return receipt, ErrConflict
		}
	}
	if receipt.State != "released" {
		if err := lockClonePostgresFenceSource(ctx, tx, op, receipt); err != nil {
			return receipt, err
		}
		q := sqlc.New()
		var row sqlc.ProjectEnvironmentClonePostgresWriteFence
		if observation == nil {
			row, err = q.BeginClonePostgresWriteFenceAbandonment(ctx, tx, sqlc.BeginClonePostgresWriteFenceAbandonmentParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token})
		} else {
			row, err = q.FinishClonePostgresWriteFenceAbandonment(ctx, tx, sqlc.FinishClonePostgresWriteFenceAbandonmentParams{
				OperationID: mustPgUUID(op.ID), SourceDatabaseID: mustPgUUID(sourceID), ExpectedRevision: op.Revision, WorkerToken: lease.Token,
				RemoteTerminalState: observation.State, RemoteReleasedAt: pgtype.Timestamptz{Time: observation.ReleasedAt, Valid: true}})
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return receipt, ErrConflict
		}
		if err != nil {
			return receipt, mapErr(err)
		}
		receipt = clonePostgresWriteFenceFromSQL(row)
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, &lease); err != nil {
		return receipt, err
	}
	return receipt, mapErr(tx.Commit(ctx))
}
