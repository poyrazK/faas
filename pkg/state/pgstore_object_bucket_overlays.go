package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ADR-20261031193635 slice 3: bucket overlays, tombstones and the buckets
// branches own, in PostgreSQL.

const objectBucketOverlayColumns = `o.bucket_id::text, o.account_id::text, o.source_bucket_id::text, o.source_app_id::text,
	o.consistency, o.as_of, o.created_at`

func (s *PgStore) scanObjectBucketOverlay(ctx context.Context, row pgx.Row) (ObjectBucketOverlay, error) {
	var o ObjectBucketOverlay
	err := row.Scan(&o.BucketID, &o.AccountID, &o.SourceBucketID, &o.SourceAppID, &o.Consistency, &o.AsOf, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectBucketOverlay{}, ErrNotFound
	}
	if err != nil {
		return ObjectBucketOverlay{}, mapErr(err)
	}
	o.AsOf, o.CreatedAt = o.AsOf.UTC(), o.CreatedAt.UTC()
	source, err := s.GetObjectBucket(ctx, o.AccountID, o.SourceAppID, o.SourceBucketID)
	switch {
	case err == nil && source.State != "deleted":
		o.Source = source
	case err != nil && !errors.Is(err, ErrNotFound):
		return ObjectBucketOverlay{}, err
	}
	return o, nil
}

func (s *PgStore) PutObjectBucketOverlay(ctx context.Context, overlay ObjectBucketOverlay) (ObjectBucketOverlay, error) {
	for _, id := range []string{overlay.BucketID, overlay.AccountID, overlay.SourceBucketID, overlay.SourceAppID} {
		if _, err := uuid.Parse(id); err != nil {
			return ObjectBucketOverlay{}, ErrInvalidArgument
		}
	}
	if overlay.SourceBucketID == overlay.BucketID || overlay.AsOf.IsZero() ||
		overlay.Consistency != ObjectBucketOverlayLiveParent && overlay.Consistency != ObjectBucketOverlayAsOf {
		return ObjectBucketOverlay{}, ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		insert into object_bucket_overlays (bucket_id, account_id, source_bucket_id, source_app_id, consistency, as_of)
		select b.id, b.account_id, $3, $4, $5, $6 from object_buckets b
		 where b.id = $1 and b.account_id = $2
		on conflict (bucket_id) do nothing
	`, overlay.BucketID, overlay.AccountID, overlay.SourceBucketID, overlay.SourceAppID, overlay.Consistency, overlay.AsOf.UTC())
	if err != nil {
		return ObjectBucketOverlay{}, mapErr(err)
	}
	stored, err := s.scanObjectBucketOverlay(ctx, s.pool.QueryRow(ctx, `select `+objectBucketOverlayColumns+`
		from object_bucket_overlays o where o.bucket_id = $1 and o.account_id = $2`, overlay.BucketID, overlay.AccountID))
	if err != nil {
		return ObjectBucketOverlay{}, err
	}
	if tag.RowsAffected() == 0 && (stored.SourceBucketID != overlay.SourceBucketID || stored.Consistency != overlay.Consistency) {
		return ObjectBucketOverlay{}, ErrConflict
	}
	return stored, nil
}

func (s *PgStore) ObjectBucketOverlay(ctx context.Context, bucketID string) (ObjectBucketOverlay, error) {
	if _, err := uuid.Parse(bucketID); err != nil {
		return ObjectBucketOverlay{}, ErrNotFound
	}
	return s.scanObjectBucketOverlay(ctx, s.pool.QueryRow(ctx, `select `+objectBucketOverlayColumns+`
		from object_bucket_overlays o join object_buckets b on b.id = o.bucket_id
		where o.bucket_id = $1 and b.state <> 'deleted'`, bucketID))
}

func (s *PgStore) AddObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) error {
	if _, err := uuid.Parse(bucketID); err != nil {
		return ErrNotFound
	}
	if !validOverlayTombstoneKeys(keys) {
		return ErrInvalidArgument
	}
	tag, err := s.pool.Exec(ctx, `
		insert into object_bucket_overlay_tombstones (bucket_id, key)
		select o.bucket_id, k.key from object_bucket_overlays o, unnest($2::text[]) as k(key)
		 where o.bucket_id = $1
		on conflict (bucket_id, key) do nothing
	`, bucketID, keys)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		var exists bool
		if err := s.pool.QueryRow(ctx, `select exists (select 1 from object_bucket_overlays where bucket_id = $1)`, bucketID).Scan(&exists); err != nil {
			return mapErr(err)
		}
		if !exists {
			return ErrNotFound
		}
	}
	return nil
}

func (s *PgStore) ObjectBucketOverlayTombstones(ctx context.Context, bucketID string, keys []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(keys) == 0 {
		return out, nil
	}
	if _, err := uuid.Parse(bucketID); err != nil {
		return out, nil
	}
	if !validOverlayTombstoneKeys(keys) {
		return nil, ErrInvalidArgument
	}
	rows, err := s.pool.Query(ctx, `select key from object_bucket_overlay_tombstones where bucket_id = $1 and key = any($2::text[])`, bucketID, keys)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, mapErr(err)
		}
		out[key] = true
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) DeleteObjectBucketOverlay(ctx context.Context, bucketID string) error {
	if _, err := uuid.Parse(bucketID); err != nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `delete from object_bucket_overlays where bucket_id = $1`, bucketID)
	return mapErr(err)
}

const projectEnvironmentBranchBucketColumns = `id::text, account_id::text, project_id::text, environment_id::text, environment_slug,
	app_id::text, source_bucket_id::text, coalesce(copy_bucket_id::text, ''), coalesce(lease_token::text, ''), created_at`

func scanProjectEnvironmentBranchBucket(row pgx.Row) (ProjectEnvironmentBranchBucket, error) {
	var b ProjectEnvironmentBranchBucket
	err := row.Scan(&b.ID, &b.AccountID, &b.ProjectID, &b.EnvironmentID, &b.EnvironmentSlug, &b.AppID, &b.SourceBucketID,
		&b.CopyBucketID, &b.LeaseToken, &b.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectEnvironmentBranchBucket{}, ErrNotFound
	}
	if err != nil {
		return ProjectEnvironmentBranchBucket{}, mapErr(err)
	}
	b.CreatedAt = b.CreatedAt.UTC()
	return b, nil
}

func (s *PgStore) RecordProjectEnvironmentBranchBucket(ctx context.Context, bucket ProjectEnvironmentBranchBucket) (ProjectEnvironmentBranchBucket, error) {
	for _, id := range []string{bucket.AccountID, bucket.ProjectID, bucket.EnvironmentID, bucket.AppID, bucket.SourceBucketID} {
		if _, err := uuid.Parse(id); err != nil {
			return ProjectEnvironmentBranchBucket{}, ErrInvalidArgument
		}
	}
	if bucket.EnvironmentSlug == "" {
		return ProjectEnvironmentBranchBucket{}, ErrInvalidArgument
	}
	recorded, err := scanProjectEnvironmentBranchBucket(s.pool.QueryRow(ctx, `
		insert into project_environment_branch_buckets
		    (account_id, project_id, environment_id, environment_slug, app_id, source_bucket_id)
		values ($1, $2, $3, $4, $5, $6)
		on conflict (environment_id, app_id, source_bucket_id) do nothing
		returning `+projectEnvironmentBranchBucketColumns,
		bucket.AccountID, bucket.ProjectID, bucket.EnvironmentID, bucket.EnvironmentSlug, bucket.AppID, bucket.SourceBucketID))
	if !errors.Is(err, ErrNotFound) {
		return recorded, err
	}
	return scanProjectEnvironmentBranchBucket(s.pool.QueryRow(ctx, `select `+projectEnvironmentBranchBucketColumns+`
		from project_environment_branch_buckets where environment_id = $1 and app_id = $2 and source_bucket_id = $3`,
		bucket.EnvironmentID, bucket.AppID, bucket.SourceBucketID))
}

func (s *PgStore) SetProjectEnvironmentBranchBucketCopy(ctx context.Context, id, copyBucketID string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(copyBucketID); err != nil {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `
		update project_environment_branch_buckets set copy_bucket_id = $2
		 where id = $1 and (copy_bucket_id is null or copy_bucket_id = $2)`, id, copyBucketID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}

func (s *PgStore) ListProjectEnvironmentBranchBuckets(ctx context.Context, environmentID string) ([]ProjectEnvironmentBranchBucket, error) {
	if _, err := uuid.Parse(environmentID); err != nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `select `+projectEnvironmentBranchBucketColumns+`
		from project_environment_branch_buckets where environment_id = $1 order by app_id, source_bucket_id`, environmentID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []ProjectEnvironmentBranchBucket
	for rows.Next() {
		b, err := scanProjectEnvironmentBranchBucket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) ClaimOrphanedProjectEnvironmentBranchBucket(ctx context.Context, leaseToken string, now time.Time, lease time.Duration) (ProjectEnvironmentBranchBucket, error) {
	if _, err := uuid.Parse(leaseToken); err != nil || lease <= 0 {
		return ProjectEnvironmentBranchBucket{}, ErrInvalidArgument
	}
	return scanProjectEnvironmentBranchBucket(s.pool.QueryRow(ctx, `
		with orphan as (
			select c.id from project_environment_branch_buckets c
			 where not exists (select 1 from project_environment_branches b where b.environment_id = c.environment_id)
			   and (c.lease_until is null or c.lease_until <= $1)
			 order by c.id
			 limit 1
			 for update skip locked
		)
		update project_environment_branch_buckets c
		   set lease_token = $2, lease_until = $3
		  from orphan where c.id = orphan.id
		returning c.id::text, c.account_id::text, c.project_id::text, c.environment_id::text, c.environment_slug,
		          c.app_id::text, c.source_bucket_id::text, coalesce(c.copy_bucket_id::text, ''), coalesce(c.lease_token::text, ''), c.created_at`,
		now.UTC(), leaseToken, now.UTC().Add(lease)))
}

func (s *PgStore) RemoveProjectEnvironmentBranchBucket(ctx context.Context, id, leaseToken string) error {
	if _, err := uuid.Parse(id); err != nil {
		return ErrNotFound
	}
	if _, err := uuid.Parse(leaseToken); err != nil {
		return ErrConflict
	}
	tag, err := s.pool.Exec(ctx, `delete from project_environment_branch_buckets where id = $1 and lease_token = $2`, id, leaseToken)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
