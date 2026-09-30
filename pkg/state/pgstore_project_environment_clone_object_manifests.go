package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ProjectEnvironmentCloneLeasedObjectManifestStore = (*PgStore)(nil)
var _ ProjectEnvironmentCloneLeasedObjectManifestStore = (*MemStore)(nil)

func (s *PgStore) PutProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID string, manifest ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error) {
	return s.putProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, manifest, nil)
}

func (s *PgStore) PutProjectEnvironmentCloneObjectManifestForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, manifest ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error) {
	if !validCloneLeaseIdentity(lease) || manifest.OperationID != lease.Operation.ID {
		return ProjectEnvironmentCloneObjectManifest{}, ErrInvalidArgument
	}
	return s.putProjectEnvironmentCloneObjectManifest(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, manifest, &lease)
}

func authorizeCloneObjectMutationTx(ctx context.Context, tx pgx.Tx, op ProjectEnvironmentCloneOperation, lease *ProjectEnvironmentCloneLease) error {
	params := sqlc.ReadProjectEnvironmentCloneObjectMutationAuthorityParams{
		OperationID: mustPgUUID(op.ID), AccountID: mustPgUUID(op.AccountID), ProjectID: mustPgUUID(op.ProjectID),
	}
	if lease != nil {
		params.WorkerToken, params.ExpectedRevision, params.ExpectedStatus = lease.Token, lease.Operation.Revision, lease.Operation.Status
	}
	authority, err := new(sqlc.Queries).ReadProjectEnvironmentCloneObjectMutationAuthority(ctx, tx, params)
	if err != nil {
		return mapErr(err)
	}
	if lease == nil && !authority.LegacyAllowed || lease != nil && !authority.WorkerAllowed {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) putProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID string, manifest ProjectEnvironmentCloneObjectManifest, lease *ProjectEnvironmentCloneLease) (ProjectEnvironmentCloneObjectManifest, error) {
	if err := validateProjectEnvironmentCloneObjectManifest(manifest); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: begin clone object manifest: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, accountID, projectID, manifest.OperationID)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, lease); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, err
	}
	if op.Status != CloneOperationCapturing && op.Status != CloneOperationCopying {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	q := new(sqlc.Queries)
	existing, err := q.ReadProjectEnvironmentCloneObjectManifestHeader(ctx, tx, sqlc.ReadProjectEnvironmentCloneObjectManifestHeaderParams{
		OperationID: mustPgUUID(manifest.OperationID), SourceBucketID: mustPgUUID(manifest.SourceBucketID), AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID),
	})
	if err == nil {
		if existing.TargetBucketID != manifest.TargetBucketID || existing.CapturedAtExact != manifest.CapturedAt.UTC().Format(time.RFC3339Nano) ||
			existing.ManifestHash != manifest.Hash || int(existing.ObjectCount) != len(manifest.Objects) {
			return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: commit clone object manifest replay: %w", err)
		}
		return s.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, manifest.OperationID, manifest.SourceBucketID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
	}
	if op.Status != CloneOperationCapturing {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	if err := q.InsertProjectEnvironmentCloneObjectManifestHeader(ctx, tx, sqlc.InsertProjectEnvironmentCloneObjectManifestHeaderParams{
		OperationID: mustPgUUID(manifest.OperationID), SourceBucketID: mustPgUUID(manifest.SourceBucketID), TargetBucketID: mustPgUUID(manifest.TargetBucketID),
		CapturedAt: pgtype.Timestamptz{Time: manifest.CapturedAt, Valid: true}, CapturedAtExact: manifest.CapturedAt.UTC().Format(time.RFC3339Nano),
		ManifestHash: manifest.Hash, ObjectCount: int32(len(manifest.Objects)),
	}); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
	}
	if len(manifest.Objects) > 0 {
		entries := make([]struct {
			Key     string                               `json:"object_key"`
			Version string                               `json:"source_version"`
			Source  ProjectEnvironmentCloneObjectVersion `json:"source_object"`
		}, len(manifest.Objects))
		for i, checkpoint := range manifest.Objects {
			entries[i].Key, entries[i].Version, entries[i].Source = checkpoint.Source.Key, checkpoint.Source.VersionID, checkpoint.Source
		}
		raw, err := json.Marshal(entries)
		if err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, err
		}
		count, err := q.InsertProjectEnvironmentCloneObjectManifestEntries(ctx, tx, sqlc.InsertProjectEnvironmentCloneObjectManifestEntriesParams{
			OperationID: mustPgUUID(manifest.OperationID), SourceBucketID: mustPgUUID(manifest.SourceBucketID), Entries: raw,
		})
		if err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
		}
		if count != int64(len(manifest.Objects)) {
			return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: commit clone object manifest: %w", err)
	}
	return cloneProjectEnvironmentCloneObjectManifest(manifest), nil
}

func (s *PgStore) ProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID, operationID, sourceBucketID string) (ProjectEnvironmentCloneObjectManifest, error) {
	return projectEnvironmentCloneObjectManifestDB(ctx, s.pool, accountID, projectID, operationID, sourceBucketID)
}

func projectEnvironmentCloneObjectManifestDB(ctx context.Context, db sqlc.DBTX, accountID, projectID, operationID, sourceBucketID string) (ProjectEnvironmentCloneObjectManifest, error) {
	q := new(sqlc.Queries)
	header, err := q.ReadProjectEnvironmentCloneObjectManifestHeader(ctx, db, sqlc.ReadProjectEnvironmentCloneObjectManifestHeaderParams{
		OperationID: mustPgUUID(operationID), SourceBucketID: mustPgUUID(sourceBucketID), AccountID: mustPgUUID(accountID), ProjectID: mustPgUUID(projectID),
	})
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
	}
	capturedAt, err := time.Parse(time.RFC3339Nano, header.CapturedAtExact)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	rows, err := q.ReadProjectEnvironmentCloneObjectManifestEntries(ctx, db, sqlc.ReadProjectEnvironmentCloneObjectManifestEntriesParams{
		OperationID: mustPgUUID(operationID), SourceBucketID: mustPgUUID(sourceBucketID),
	})
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: load clone object manifest entries: %w", err)
	}
	if len(rows) != int(header.ObjectCount) {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	manifest := ProjectEnvironmentCloneObjectManifest{OperationID: header.OperationID, SourceBucketID: header.SourceBucketID,
		TargetBucketID: header.TargetBucketID, CapturedAt: capturedAt, Hash: header.ManifestHash,
		Objects: make([]ProjectEnvironmentCloneObjectCheckpoint, len(rows))}
	for i, row := range rows {
		item := ProjectEnvironmentCloneObjectCheckpoint{TargetETag: row.TargetEtag, VerifiedSHA256: row.VerifiedSha256}
		if err := json.Unmarshal(row.SourceObject, &item.Source); err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
		}
		if row.CopiedAt.Valid {
			stamp := row.CopiedAt.Time
			item.CopiedAt = &stamp
		}
		manifest.Objects[i] = item
	}
	return manifest, nil
}

func (s *PgStore) MarkProjectEnvironmentCloneObjectCopied(ctx context.Context, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
	return s.markProjectEnvironmentCloneObjectCopied(ctx, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256, nil)
}

func (s *PgStore) MarkProjectEnvironmentCloneObjectCopiedForLease(ctx context.Context, lease ProjectEnvironmentCloneLease, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
	if !validCloneLeaseIdentity(lease) {
		return ErrInvalidArgument
	}
	return s.markProjectEnvironmentCloneObjectCopied(ctx, lease.Operation.AccountID, lease.Operation.ProjectID, lease.Operation.ID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256, &lease)
}

func (s *PgStore) markProjectEnvironmentCloneObjectCopied(ctx context.Context, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string, lease *ProjectEnvironmentCloneLease) error {
	if !validCloneObjectKey(key) || sourceVersion == "" || targetETag == "" || !validCloneObjectSHA256(verifiedSHA256) {
		return ErrInvalidProjectEnvironmentCloneOperation
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin clone object checkpoint: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	op, err := lockCloneWorkloadOperationTx(ctx, tx, accountID, projectID, operationID)
	if err != nil {
		return err
	}
	if err := authorizeCloneObjectMutationTx(ctx, tx, op, lease); err != nil {
		return err
	}
	if op.Status != CloneOperationCopying {
		return ErrConflict
	}
	count, err := new(sqlc.Queries).MarkProjectEnvironmentCloneObjectManifestEntryCopied(ctx, tx, sqlc.MarkProjectEnvironmentCloneObjectManifestEntryCopiedParams{
		OperationID: mustPgUUID(operationID), SourceBucketID: mustPgUUID(sourceBucketID), ObjectKey: key, SourceVersion: sourceVersion,
		TargetEtag: targetETag, VerifiedSha256: verifiedSHA256,
	})
	if err != nil {
		return mapErr(err)
	}
	if count == 1 {
		return tx.Commit(ctx)
	}
	if err := tx.Rollback(ctx); err != nil {
		return err
	}
	if _, lookupErr := s.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, operationID, sourceBucketID); lookupErr != nil {
		return lookupErr
	}
	return ErrConflict
}
