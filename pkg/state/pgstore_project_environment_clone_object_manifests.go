package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) PutProjectEnvironmentCloneObjectManifest(ctx context.Context, accountID, projectID string, manifest ProjectEnvironmentCloneObjectManifest) (ProjectEnvironmentCloneObjectManifest, error) {
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
	status := op.Status
	if status != CloneOperationCapturing && status != CloneOperationCopying {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	var existingTarget, existingHash string
	var existingCapture string
	var existingCount int
	err = tx.QueryRow(ctx, `select target_bucket_id::text, captured_at_exact, manifest_hash, object_count
		from project_environment_clone_object_manifests
		where operation_id = $1 and source_bucket_id = $2`,
		manifest.OperationID, manifest.SourceBucketID).Scan(&existingTarget, &existingCapture, &existingHash, &existingCount)
	if err == nil {
		if existingTarget != manifest.TargetBucketID || existingCapture != manifest.CapturedAt.UTC().Format(time.RFC3339Nano) ||
			existingHash != manifest.Hash || existingCount != len(manifest.Objects) {
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
	if status != CloneOperationCapturing {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	if _, err := tx.Exec(ctx, `insert into project_environment_clone_object_manifests
		(operation_id, source_bucket_id, target_bucket_id, captured_at, captured_at_exact, manifest_hash, object_count)
		values ($1, $2, $3, $4, $5, $6, $7)`, manifest.OperationID, manifest.SourceBucketID,
		manifest.TargetBucketID, manifest.CapturedAt, manifest.CapturedAt.UTC().Format(time.RFC3339Nano), manifest.Hash, len(manifest.Objects)); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
	}
	if len(manifest.Objects) > 0 {
		count, err := tx.CopyFrom(ctx,
			pgx.Identifier{"project_environment_clone_object_entries"},
			[]string{"operation_id", "source_bucket_id", "object_key", "source_version", "source_object"},
			pgx.CopyFromSlice(len(manifest.Objects), func(i int) ([]any, error) {
				item := manifest.Objects[i].Source
				raw, err := json.Marshal(item)
				if err != nil {
					return nil, err
				}
				return []any{manifest.OperationID, manifest.SourceBucketID, item.Key, item.VersionID, raw}, nil
			}))
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
	var manifest ProjectEnvironmentCloneObjectManifest
	var count int
	var capturedAt string
	err := s.pool.QueryRow(ctx, `select m.operation_id::text, m.source_bucket_id::text, m.target_bucket_id::text,
		m.captured_at_exact, m.manifest_hash, m.object_count
		from project_environment_clone_object_manifests m
		join project_environment_clone_operations o on o.id = m.operation_id
		where m.operation_id = $1 and m.source_bucket_id = $2 and o.account_id = $3 and o.project_id = $4`,
		operationID, sourceBucketID, accountID, projectID).Scan(&manifest.OperationID, &manifest.SourceBucketID,
		&manifest.TargetBucketID, &capturedAt, &manifest.Hash, &count)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
	}
	manifest.CapturedAt, err = time.Parse(time.RFC3339Nano, capturedAt)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	rows, err := s.pool.Query(ctx, `select source_object, copied_at, target_etag, verified_sha256
		from project_environment_clone_object_entries
		where operation_id = $1 and source_bucket_id = $2 order by object_key`, operationID, sourceBucketID)
	if err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: load clone object manifest entries: %w", err)
	}
	defer rows.Close()
	manifest.Objects = make([]ProjectEnvironmentCloneObjectCheckpoint, 0, count)
	for rows.Next() {
		var item ProjectEnvironmentCloneObjectCheckpoint
		var raw []byte
		if err := rows.Scan(&raw, &item.CopiedAt, &item.TargetETag, &item.VerifiedSHA256); err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, mapErr(err)
		}
		if err := json.Unmarshal(raw, &item.Source); err != nil {
			return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
		}
		manifest.Objects = append(manifest.Objects, item)
	}
	if err := rows.Err(); err != nil {
		return ProjectEnvironmentCloneObjectManifest{}, fmt.Errorf("state: load clone object manifest rows: %w", err)
	}
	if len(manifest.Objects) != count {
		return ProjectEnvironmentCloneObjectManifest{}, ErrConflict
	}
	return manifest, nil
}

func (s *PgStore) MarkProjectEnvironmentCloneObjectCopied(ctx context.Context, accountID, projectID, operationID, sourceBucketID, key, sourceVersion, targetETag, verifiedSHA256 string) error {
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
	if op.Status != CloneOperationCopying {
		return ErrConflict
	}
	var copiedAt time.Time
	err = tx.QueryRow(ctx, `update project_environment_clone_object_entries e
		set copied_at = coalesce(e.copied_at, now()), target_etag = $7, verified_sha256 = $8
		from project_environment_clone_operations o
		where e.operation_id = $1 and e.source_bucket_id = $2 and e.object_key = $3
		  and e.source_version = $4 and o.id = e.operation_id
		  and o.account_id = $5 and o.project_id = $6 and o.status = 'copying'
		  and (e.copied_at is null or (e.target_etag = $7 and e.verified_sha256 = $8))
		returning e.copied_at`, operationID, sourceBucketID, key, sourceVersion,
		accountID, projectID, targetETag, verifiedSHA256).Scan(&copiedAt)
	if err == nil {
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return mapErr(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		return err
	}
	if _, lookupErr := s.ProjectEnvironmentCloneObjectManifest(ctx, accountID, projectID, operationID, sourceBucketID); lookupErr != nil {
		return lookupErr
	}
	return ErrConflict
}
