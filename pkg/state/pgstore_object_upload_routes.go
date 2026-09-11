package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var _ ObjectUploadRouteStore = (*PgStore)(nil)

func scanObjectUploadRoute(row pgx.Row) (ObjectUploadRoute, error) {
	var id, accountID, appID, bucketID pgtype.UUID
	var route ObjectUploadRoute
	if err := row.Scan(&id, &accountID, &appID, &route.Name, &bucketID, &route.KeyPrefix, &route.MaxBytes, &route.AllowedContentTypes, &route.Enabled, &route.CreatedAt, &route.UpdatedAt); err != nil {
		return ObjectUploadRoute{}, mapErr(err)
	}
	route.ID, route.AccountID, route.AppID, route.BucketID = pgUUIDString(id), pgUUIDString(accountID), pgUUIDString(appID), pgUUIDString(bucketID)
	return route, nil
}

func (s *PgStore) ListObjectUploadRoutes(ctx context.Context, accountID, appID string) ([]ObjectUploadRoute, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, app_id, name, bucket_id, key_prefix, max_bytes,
		       allowed_content_types, enabled, created_at, updated_at
		  FROM object_upload_routes
		 WHERE account_id=$1 AND app_id=$2
		 ORDER BY name`, mustPgUUID(accountID), mustPgUUID(appID))
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]ObjectUploadRoute, 0)
	for rows.Next() {
		var id, account, app, bucket pgtype.UUID
		var route ObjectUploadRoute
		if err := rows.Scan(&id, &account, &app, &route.Name, &bucket, &route.KeyPrefix, &route.MaxBytes, &route.AllowedContentTypes, &route.Enabled, &route.CreatedAt, &route.UpdatedAt); err != nil {
			return nil, mapErr(err)
		}
		route.ID, route.AccountID, route.AppID, route.BucketID = pgUUIDString(id), pgUUIDString(account), pgUUIDString(app), pgUUIDString(bucket)
		out = append(out, route)
	}
	return out, mapErr(rows.Err())
}

func (s *PgStore) GetObjectUploadRoute(ctx context.Context, accountID, appID, name string) (ObjectUploadRoute, error) {
	return scanObjectUploadRoute(s.pool.QueryRow(ctx, `
		SELECT id, account_id, app_id, name, bucket_id, key_prefix, max_bytes,
		       allowed_content_types, enabled, created_at, updated_at
		  FROM object_upload_routes
		 WHERE account_id=$1 AND app_id=$2 AND name=$3`, mustPgUUID(accountID), mustPgUUID(appID), name))
}

func (s *PgStore) UpsertObjectUploadRoute(ctx context.Context, route ObjectUploadRoute) (ObjectUploadRoute, error) {
	return scanObjectUploadRoute(s.pool.QueryRow(ctx, `
		INSERT INTO object_upload_routes
			(id, account_id, app_id, name, bucket_id, key_prefix, max_bytes, allowed_content_types, enabled)
		SELECT $1,$2,$3,$4,$5,$6,$7,$8,$9
		 WHERE EXISTS (
			SELECT 1 FROM object_buckets
			 WHERE id=$5 AND account_id=$2 AND app_id=$3 AND state='ready'
		 )
		ON CONFLICT (app_id, name) DO UPDATE SET
			bucket_id=EXCLUDED.bucket_id,
			key_prefix=EXCLUDED.key_prefix,
			max_bytes=EXCLUDED.max_bytes,
			allowed_content_types=EXCLUDED.allowed_content_types,
			enabled=EXCLUDED.enabled,
			updated_at=now()
		RETURNING id, account_id, app_id, name, bucket_id, key_prefix, max_bytes,
		          allowed_content_types, enabled, created_at, updated_at`,
		mustPgUUID(route.ID), mustPgUUID(route.AccountID), mustPgUUID(route.AppID), route.Name,
		mustPgUUID(route.BucketID), route.KeyPrefix, route.MaxBytes, route.AllowedContentTypes, route.Enabled))
}

func (s *PgStore) DeleteObjectUploadRoute(ctx context.Context, accountID, appID, name string) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM object_upload_routes WHERE account_id=$1 AND app_id=$2 AND name=$3`, mustPgUUID(accountID), mustPgUUID(appID), name)
	if err != nil {
		return mapErr(err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) RecordObjectUploadCompletion(ctx context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	var id, routeID, accountID, appID, bucketID pgtype.UUID
	var out ObjectUploadCompletion
	err := s.pool.QueryRow(ctx, `
		INSERT INTO object_upload_completions
			(id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
			 bytes, content_type, etag, status, error_code, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		RETURNING id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
		          bytes, content_type, etag, status, error_code, request_id, created_at`,
		mustPgUUID(completion.ID), mustPgUUID(completion.RouteID), mustPgUUID(completion.AccountID),
		mustPgUUID(completion.AppID), mustPgUUID(completion.BucketID), completion.SubjectID, completion.Key,
		completion.Bytes, completion.ContentType, completion.ETag, completion.Status, completion.ErrorCode, completion.RequestID).
		Scan(&id, &routeID, &accountID, &appID, &bucketID, &out.SubjectID, &out.Key, &out.Bytes, &out.ContentType, &out.ETag, &out.Status, &out.ErrorCode, &out.RequestID, &out.CreatedAt)
	if err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	out.ID, out.RouteID, out.AccountID, out.AppID, out.BucketID = pgUUIDString(id), pgUUIDString(routeID), pgUUIDString(accountID), pgUUIDString(appID), pgUUIDString(bucketID)
	return out, nil
}
