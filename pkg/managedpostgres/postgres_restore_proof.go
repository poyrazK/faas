package managedpostgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ CloneRestoreProofStore = (*PostgresStore)(nil)

func (s *PostgresStore) FinishCloneRestoreProvision(ctx context.Context, database Database, observed ObservedDatabase, now time.Time) (Database, error) {
	proof, err := newCloneRestoreProof(database, observed, now)
	if err != nil || database.LeaseToken == "" {
		if err == nil {
			err = ErrInvalid
		}
		return Database{}, err
	}
	ids := make([]pgtype.UUID, 0, 4)
	for _, value := range []string{proof.DatabaseID, proof.AccountID, proof.OperationID, proof.SourceDatabaseID} {
		id, err := postgresUUID(value)
		if err != nil {
			return Database{}, err
		}
		ids = append(ids, id)
	}
	spec, err := json.Marshal(proof.Spec)
	if err != nil {
		return Database{}, ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Database{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := new(sqlc.Queries)
	actual, err := q.ReadProjectEnvironmentCloneDatabaseSource(ctx, tx, sqlc.ReadProjectEnvironmentCloneDatabaseSourceParams{AccountID: ids[1], ID: ids[0]})
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	// Read the server clock after the row lock, including an unchanged row
	// held by another transaction. A pre-lock timestamp is not lease authority.
	clock, err := q.ReadProjectEnvironmentCloneDatabaseReservationTime(ctx, tx)
	if err != nil {
		return Database{}, err
	}
	if !actual.LeaseUntil.Valid || !actual.LeaseUntil.Time.After(clock.Time) || proof.Lineage.PointInTime.After(clock.Time) {
		return Database{}, ErrConflict
	}
	proof.ObservedAt = clock.Time
	_, err = q.FinishManagedPostgresCloneRestoreWithProof(ctx, tx, sqlc.FinishManagedPostgresCloneRestoreWithProofParams{
		DatabaseID: ids[0], AccountID: ids[1], OperationID: ids[2], SourceDatabaseID: ids[3], LeaseToken: database.LeaseToken,
		BackendID: proof.BackendID, BackendFingerprint: proof.BackendFingerprint, ProviderResourceID: proof.ProviderResourceID, DataResourceID: proof.DataResourceID,
		SourceResourceID: proof.Lineage.SourceResourceID, PointInTime: pgtype.Timestamptz{Time: proof.Lineage.PointInTime, Valid: true},
		Spec: spec, Generation: proof.Generation, ObservedAt: pgtype.Timestamptz{Time: proof.ObservedAt, Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		return Database{}, ErrConflict
	}
	if err != nil {
		return Database{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Database{}, mapPostgresError(err)
	}
	return s.Get(ctx, database.AccountID, database.ID)
}

func (s *PostgresStore) GetCloneRestoreProof(ctx context.Context, accountID, databaseID string) (RestoreProof, error) {
	account, err := postgresUUID(accountID)
	if err != nil {
		return RestoreProof{}, err
	}
	id, err := postgresUUID(databaseID)
	if err != nil {
		return RestoreProof{}, err
	}
	row, err := new(sqlc.Queries).ReadManagedPostgresCloneRestoreProof(ctx, s.pool, sqlc.ReadManagedPostgresCloneRestoreProofParams{AccountID: account, DatabaseID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return RestoreProof{}, ErrConflict
	}
	if err != nil {
		return RestoreProof{}, mapPostgresError(err)
	}
	proof := RestoreProof{DatabaseID: uuid.UUID(row.DatabaseID.Bytes).String(), AccountID: uuid.UUID(row.AccountID.Bytes).String(), OperationID: uuid.UUID(row.OperationID.Bytes).String(),
		BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, ProviderResourceID: row.ProviderResourceID, DataResourceID: row.DataResourceID.String,
		SourceDatabaseID: uuid.UUID(row.SourceDatabaseID.Bytes).String(), Lineage: RestoreLineage{SourceResourceID: row.SourceResourceID, PointInTime: row.PointInTime.Time},
		Generation: row.Generation, ObservedAt: row.ObservedAt.Time}
	if err := json.Unmarshal(row.Spec, &proof.Spec); err != nil || proof.Spec.Validate() != nil {
		return RestoreProof{}, ErrConflict
	}
	return proof, nil
}
