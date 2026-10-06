package state

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectUploadRouteStore = (*PgStore)(nil)

func objectUploadRouteFromSQL(row sqlc.ObjectUploadRoute) (ObjectUploadRoute, error) {
	route := ObjectUploadRoute{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), Name: row.Name, BucketID: pgUUIDString(row.BucketID), KeyPrefix: row.KeyPrefix, MaxBytes: row.MaxBytes, AllowedContentTypes: row.AllowedContentTypes, Enabled: row.Enabled, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	var err error
	route.Encryption, err = encryptionSnapshotFromJSON(row.EncryptionSnapshot, route.AccountID)
	return route, err
}

func (s *PgStore) ListObjectUploadRoutes(ctx context.Context, accountID, appID string) ([]ObjectUploadRoute, error) {
	rows, err := sqlc.New().ObjectUploadRoutesList(ctx, s.pool, sqlc.ObjectUploadRoutesListParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]ObjectUploadRoute, 0, len(rows))
	for _, row := range rows {
		route, e := objectUploadRouteFromSQL(row)
		if e != nil {
			return nil, e
		}
		out = append(out, route)
	}
	return out, nil
}

func (s *PgStore) GetObjectUploadRoute(ctx context.Context, accountID, appID, name string) (ObjectUploadRoute, error) {
	row, err := sqlc.New().ObjectUploadRouteGet(ctx, s.pool, sqlc.ObjectUploadRouteGetParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Name: name})
	if err != nil {
		return ObjectUploadRoute{}, mapErr(err)
	}
	return objectUploadRouteFromSQL(row)
}

func (s *PgStore) UpsertObjectUploadRoute(ctx context.Context, route ObjectUploadRoute) (ObjectUploadRoute, error) {
	if _, err := s.GetObjectBucket(ctx, route.AccountID, route.AppID, route.BucketID); err != nil {
		return ObjectUploadRoute{}, err
	}
	if !route.Encryption.ValidFor(route.AccountID) {
		return ObjectUploadRoute{}, ErrConflict
	}
	snapshot, err := encryptionSnapshotJSON(route.Encryption)
	if err != nil {
		return ObjectUploadRoute{}, err
	}
	row, err := sqlc.New().ObjectUploadRouteUpsert(ctx, s.pool, sqlc.ObjectUploadRouteUpsertParams{ID: mustPgUUID(route.ID), AccountID: mustPgUUID(route.AccountID), AppID: mustPgUUID(route.AppID), Name: route.Name, BucketID: mustPgUUID(route.BucketID), KeyPrefix: route.KeyPrefix, MaxBytes: route.MaxBytes, AllowedContentTypes: route.AllowedContentTypes, Enabled: route.Enabled, EncryptionSnapshot: snapshot})
	if err != nil {
		return ObjectUploadRoute{}, mapErr(err)
	}
	return objectUploadRouteFromSQL(row)
}

func (s *PgStore) DeleteObjectUploadRoute(ctx context.Context, accountID, appID, name string) error {
	n, err := sqlc.New().ObjectUploadRouteDelete(ctx, s.pool, sqlc.ObjectUploadRouteDeleteParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), Name: name})
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) RecordObjectUploadCompletion(ctx context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !emptyCopySourceProvenance(completion) || !completion.Encryption.Empty() || !completion.VerifiedEncryption.Empty() {
		return ObjectUploadCompletion{}, ErrConflict
	}
	return scanObjectUploadCompletion(s.pool.QueryRow(ctx, `
		INSERT INTO object_upload_completions
			(id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
			 bytes, content_type, etag, status, error_code, request_id, idempotency_key, request_fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
		          bytes, content_type, etag, status, error_code, request_id, idempotency_key, request_fingerprint, created_at`,
		mustPgUUID(completion.ID), mustPgUUID(completion.RouteID), mustPgUUID(completion.AccountID),
		mustPgUUID(completion.AppID), mustPgUUID(completion.BucketID), completion.SubjectID, completion.Key,
		completion.Bytes, completion.ContentType, completion.ETag, completion.Status, completion.ErrorCode, completion.RequestID,
		completion.IdempotencyKey, completion.RequestFingerprint))
}

func (s *PgStore) CreateObjectUploadIntent(ctx context.Context, intent ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	if !emptyCopySourceProvenance(intent) || !intent.Encryption.Empty() || !intent.VerifiedEncryption.Empty() || intent.IdempotencyKey == "" || intent.RequestFingerprint == "" || intent.Status != "pending" {
		return ObjectUploadCompletion{}, ErrConflict
	}
	return scanObjectUploadCompletion(s.pool.QueryRow(ctx, `
		INSERT INTO object_upload_completions
			(id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
			 bytes, content_type, etag, status, error_code, request_id, idempotency_key, request_fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
		RETURNING id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
		          bytes, content_type, etag, status, error_code, request_id, idempotency_key, request_fingerprint, created_at`,
		mustPgUUID(intent.ID), mustPgUUID(intent.RouteID), mustPgUUID(intent.AccountID),
		mustPgUUID(intent.AppID), mustPgUUID(intent.BucketID), intent.SubjectID, intent.Key,
		intent.Bytes, intent.ContentType, intent.ETag, intent.Status, intent.ErrorCode, intent.RequestID,
		intent.IdempotencyKey, intent.RequestFingerprint))
}

func (s *PgStore) GetObjectUploadIntent(ctx context.Context, routeID, subjectID, idempotencyKey string) (ObjectUploadCompletion, error) {
	row, err := sqlc.New().ObjectUploadIntentGet(ctx, s.pool, sqlc.ObjectUploadIntentGetParams{RouteID: mustPgUUID(routeID), SubjectID: subjectID, IdempotencyKey: idempotencyKey})
	if err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	return objectTrackedUploadFromSQL(row)
}

func (s *PgStore) UpdateObjectUploadCompletion(ctx context.Context, completion ObjectUploadCompletion) (ObjectUploadCompletion, error) {
	return scanObjectUploadCompletion(s.pool.QueryRow(ctx, `
		UPDATE object_upload_completions
		   SET etag=$2, status=$3, error_code=$4, request_id=$5
		 WHERE id=$1 AND idempotency_key <> '' AND write_phase='untracked'
		RETURNING id, route_id, account_id, app_id, bucket_id, subject_id, object_key,
		          bytes, content_type, etag, status, error_code, request_id, idempotency_key, request_fingerprint, created_at`,
		mustPgUUID(completion.ID), completion.ETag, completion.Status, completion.ErrorCode, completion.RequestID))
}

func scanObjectUploadCompletion(row pgx.Row) (ObjectUploadCompletion, error) {
	var id, routeID, accountID, appID, bucketID pgtype.UUID
	var out ObjectUploadCompletion
	err := row.Scan(&id, &routeID, &accountID, &appID, &bucketID, &out.SubjectID, &out.Key, &out.Bytes,
		&out.ContentType, &out.ETag, &out.Status, &out.ErrorCode, &out.RequestID, &out.IdempotencyKey,
		&out.RequestFingerprint, &out.CreatedAt)
	if err != nil {
		return ObjectUploadCompletion{}, mapErr(err)
	}
	out.ID, out.RouteID, out.AccountID, out.AppID, out.BucketID = pgUUIDString(id), pgUUIDString(routeID), pgUUIDString(accountID), pgUUIDString(appID), pgUUIDString(bucketID)
	return out, nil
}
