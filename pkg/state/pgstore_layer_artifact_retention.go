package state

import (
	"context"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func lockLayerArtifactTx(ctx context.Context, tx pgx.Tx, key string) (LayerArtifactDeletion, error) {
	if !validLayerArtifactKey(key) {
		return LayerArtifactDeletion{}, ErrInvalidArgument
	}
	q := new(sqlc.Queries)
	if err := q.RegisterLayerArtifactRetention(ctx, tx, key); err != nil {
		return LayerArtifactDeletion{}, mapProjectCloneSnapshotErr(err)
	}
	row, err := q.LockLayerArtifactRetention(ctx, tx, key)
	return LayerArtifactDeletion{StorageKey: row.StorageKey, State: row.State, DeletionID: row.DeletionID}, mapProjectCloneSnapshotErr(err)
}

func requireLayerArtifactsRetainedTx(ctx context.Context, tx pgx.Tx, keys []string) error {
	sort.Strings(keys)
	for _, key := range keys {
		row, err := lockLayerArtifactTx(ctx, tx, key)
		if err != nil {
			return err
		}
		if row.State != LayerArtifactRetained {
			return ErrLayerArtifactRetired
		}
	}
	return nil
}

func requireDeploymentLayerArtifactsTx(ctx context.Context, tx pgx.Tx, deploymentID string) error {
	keys, err := new(sqlc.Queries).ReadDeploymentLayerArtifactKeys(ctx, tx, mustPgUUID(deploymentID))
	if err != nil {
		return mapErr(err)
	}
	return requireLayerArtifactsRetainedTx(ctx, tx, keys)
}

func pinCloneLayerArtifactsTx(ctx context.Context, tx pgx.Tx, records []projectCloneWorkloadRecord) error {
	var keys []string
	for _, record := range records {
		for key := range cloneLayerArtifacts(record) {
			keys = append(keys, key)
		}
	}
	if err := requireLayerArtifactsRetainedTx(ctx, tx, keys); err != nil {
		return err
	}
	q := new(sqlc.Queries)
	for _, record := range records {
		for key, bytes := range cloneLayerArtifacts(record) {
			if err := q.InsertProjectEnvironmentCloneLayerPin(ctx, tx, sqlc.InsertProjectEnvironmentCloneLayerPinParams{
				OperationID: mustPgUUID(record.OperationID), AppID: mustPgUUID(record.AppID), StorageKey: key, Bytes: bytes,
			}); err != nil {
				return mapErr(err)
			}
		}
	}
	return nil
}

func (s *PgStore) ClaimLayerArtifactDeletion(ctx context.Context, key string) (LayerArtifactDeletion, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return LayerArtifactDeletion{}, false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	row, err := lockLayerArtifactTx(ctx, tx, key)
	if err != nil {
		return row, false, err
	}
	if row.State == LayerArtifactDeleted {
		return row, true, nil
	}
	q := new(sqlc.Queries)
	if err := q.RequestLayerArtifactDeletion(ctx, tx, key); err != nil {
		return row, false, mapErr(err)
	}
	if row.State == LayerArtifactRetained {
		referenced, err := q.LayerArtifactHasReferences(ctx, tx, key)
		if err != nil {
			return row, false, mapErr(err)
		}
		if !referenced.Valid {
			return row, false, ErrConflict
		}
		if referenced.Bool {
			return row, false, tx.Commit(ctx)
		}
		row.State, row.DeletionID = LayerArtifactDeleting, uuid.NewString()
		count, err := q.ClaimLayerArtifactDeletion(ctx, tx, sqlc.ClaimLayerArtifactDeletionParams{StorageKey: key, DeletionID: mustPgUUID(row.DeletionID)})
		if err != nil {
			return row, false, mapErr(err)
		}
		if count != 1 {
			return row, false, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return row, false, err
	}
	return row, true, nil
}

func (s *PgStore) CompleteLayerArtifactDeletion(ctx context.Context, claim LayerArtifactDeletion) error {
	if !validLayerArtifactKey(claim.StorageKey) || !validCloneLeaseToken(claim.DeletionID) {
		return ErrInvalidArgument
	}
	count, err := new(sqlc.Queries).CompleteLayerArtifactDeletion(ctx, s.pool, sqlc.CompleteLayerArtifactDeletionParams{
		StorageKey: claim.StorageKey, DeletionID: mustPgUUID(claim.DeletionID),
	})
	if err != nil {
		return mapErr(err)
	}
	if count != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) PendingLayerArtifactDeletions(ctx context.Context) ([]LayerArtifactDeletion, error) {
	rows, err := new(sqlc.Queries).PendingLayerArtifactDeletions(ctx, s.pool)
	if err != nil {
		return nil, mapErr(err)
	}
	claims := make([]LayerArtifactDeletion, len(rows))
	for i, row := range rows {
		claims[i] = LayerArtifactDeletion{StorageKey: row.StorageKey, State: row.State, DeletionID: row.DeletionID}
	}
	return claims, nil
}
