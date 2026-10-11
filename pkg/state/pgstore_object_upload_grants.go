package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectUploadGrantStore = (*PgStore)(nil)

func objectUploadGrantFromSQL(row sqlc.ObjectStorageUploadGrant) (ObjectUploadGrant, error) {
	var headers map[string]string
	if err := json.Unmarshal(row.Headers, &headers); err != nil || headers == nil {
		return ObjectUploadGrant{}, ErrConflict
	}
	return ObjectUploadGrant{ID: pgUUIDString(row.ID), TokenHash: row.TokenHash, Kind: row.Kind, Key: row.ObjectKey,
		Bucket: ObjectBucket{ID: pgUUIDString(row.BucketID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
			BackendID: row.BackendID, BackendFingerprint: row.BackendFingerprint, PhysicalName: row.PhysicalName},
		SizeBytes: row.SizeBytes, Headers: headers, UploadID: pgUUIDStringNullable(row.UploadID), ProviderUploadID: row.ProviderUploadID.String,
		PartNumber: row.PartNumber, CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time}, nil
}

func (s *PgStore) CreateObjectUploadGrant(ctx context.Context, g ObjectUploadGrant, ttl int) (ObjectUploadGrant, error) {
	if !validObjectUploadGrant(g, ttl) {
		return ObjectUploadGrant{}, ErrInvalidArgument
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ObjectUploadGrant{}, mapErr(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	b, err := lockObjectMutationBucket(ctx, tx, g.Bucket)
	if err != nil {
		return ObjectUploadGrant{}, err
	}
	q := sqlc.New()
	_, err = q.ObjectBucketGet(ctx, tx, sqlc.ObjectBucketGetParams{AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), ID: mustPgUUID(b.ID)})
	if err != nil {
		return ObjectUploadGrant{}, mapErr(err)
	}
	_, err = q.ObjectBucketWriteFenceRead(ctx, tx, mustPgUUID(b.ID))
	if err == nil {
		return ObjectUploadGrant{}, ErrObjectBucketWriteFenced
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return ObjectUploadGrant{}, mapErr(err)
	}
	var expiryCap pgtype.Timestamptz
	if g.Kind == ObjectUploadGrantMultipartPart {
		// The SQL resolve predicate rechecks this state for every request.
		u, err := q.ObjectMultipartGet(ctx, tx, sqlc.ObjectMultipartGetParams{AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID), BucketID: mustPgUUID(b.ID), ID: mustPgUUID(g.UploadID)})
		if err != nil {
			return ObjectUploadGrant{}, mapErr(err)
		}
		clock, err := q.ObjectUploadGrantClock(ctx, tx)
		if err != nil {
			return ObjectUploadGrant{}, mapErr(err)
		}
		upload, err := objectMultipartFromSQL(u)
		if err != nil {
			return ObjectUploadGrant{}, err
		}
		if !validObjectUploadGrantPart(g, upload, clock.Time) {
			return ObjectUploadGrant{}, ErrConflict
		}
		expiryCap = u.ExpiresAt
	}
	headers, err := json.Marshal(g.Headers)
	if err != nil {
		return ObjectUploadGrant{}, ErrInvalidArgument
	}
	var uploadID pgtype.UUID
	if g.UploadID != "" {
		uploadID = mustPgUUID(g.UploadID)
	}
	row, err := q.ObjectUploadGrantInsert(ctx, tx, sqlc.ObjectUploadGrantInsertParams{
		ID: mustPgUUID(g.ID), BucketID: mustPgUUID(b.ID), AccountID: mustPgUUID(b.AccountID), AppID: mustPgUUID(b.AppID),
		TokenHash: g.TokenHash, Kind: g.Kind, ObjectKey: g.Key, SizeBytes: g.SizeBytes, Headers: headers, UploadID: uploadID,
		ProviderUploadID: pgtype.Text{String: g.ProviderUploadID, Valid: g.ProviderUploadID != ""}, PartNumber: g.PartNumber,
		BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: b.PhysicalName, TtlSeconds: int32(ttl), ExpiryCap: expiryCap})
	if err != nil {
		return ObjectUploadGrant{}, mapErr(err)
	}
	created, err := objectUploadGrantFromSQL(row)
	if err != nil {
		return ObjectUploadGrant{}, err
	}
	created.Bucket = b
	return created, mapErr(tx.Commit(ctx))
}

func (s *PgStore) ResolveObjectUploadGrant(ctx context.Context, hash string) (ObjectUploadGrant, error) {
	if !validObjectUploadTokenHash(hash) {
		return ObjectUploadGrant{}, ErrNotFound
	}
	q := sqlc.New()
	row, err := q.ObjectUploadGrantResolve(ctx, s.pool, hash)
	if err != nil {
		return ObjectUploadGrant{}, mapErr(err)
	}
	g, err := objectUploadGrantFromSQL(row)
	if err != nil {
		return ObjectUploadGrant{}, err
	}
	b, err := q.ObjectBucketGet(ctx, s.pool, sqlc.ObjectBucketGetParams{AccountID: mustPgUUID(g.Bucket.AccountID), AppID: mustPgUUID(g.Bucket.AppID), ID: mustPgUUID(g.Bucket.ID)})
	if err != nil {
		return ObjectUploadGrant{}, mapErr(err)
	}
	live := objectBucketFromSQL(b)
	if !sameObjectMutationBucket(g.Bucket, live) {
		return ObjectUploadGrant{}, ErrNotFound
	}
	g.Bucket = live
	return g, nil
}

func (s *PgStore) PruneExpiredObjectUploadGrants(ctx context.Context, limit int32) (int64, error) {
	if limit < 1 || limit > api.ObjectUploadGrantPruneBatch {
		return 0, ErrInvalidArgument
	}
	n, err := sqlc.New().ObjectUploadGrantPrune(ctx, s.pool, limit)
	return n, mapErr(err)
}
