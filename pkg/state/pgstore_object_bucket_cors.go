package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectBucketCORSStore = (*PgStore)(nil)
var _ ObjectBucketCORSReader = (*PgStore)(nil)

func decodeObjectBucketCORSRules(raw []byte) ([]api.ObjectBucketCORSRule, error) {
	var rules []api.ObjectBucketCORSRule
	if err := json.Unmarshal(raw, &rules); err != nil || !api.ValidObjectBucketCORSRules(rules) {
		return nil, ErrConflict
	}
	return rules, nil
}

func (s *pgCore) GetObjectBucketCORS(ctx context.Context, account, app, bucket string) (api.ObjectBucketCORS, error) {
	if _, err := s.GetObjectBucket(ctx, account, app, bucket); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	r, err := sqlc.New().ObjectBucketCORSGet(ctx, s.pool, mustPgUUID(bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ObjectBucketCORS{BucketID: bucket, Rules: []api.ObjectBucketCORSRule{}}, nil
	}
	if err != nil {
		return api.ObjectBucketCORS{}, mapErr(err)
	}
	rules, err := decodeObjectBucketCORSRules(r.Rules)
	if err != nil {
		return api.ObjectBucketCORS{}, err
	}
	return api.ObjectBucketCORS{BucketID: bucket, Rules: rules, Revision: r.Revision, UpdatedAt: r.UpdatedAt.Time.UTC()}, nil
}

func (s *pgCore) PutObjectBucketCORS(ctx context.Context, account, app, bucket string, rules []api.ObjectBucketCORSRule) (api.ObjectBucketCORS, error) {
	if !api.ValidObjectBucketCORSRules(rules) {
		return api.ObjectBucketCORS{}, ErrConflict
	}
	raw, err := json.Marshal(rules)
	if err != nil || len(raw) > api.MaxObjectBucketCORSBodyBytes {
		return api.ObjectBucketCORS{}, ErrConflict
	}
	if _, err = s.GetObjectBucket(ctx, account, app, bucket); err != nil {
		return api.ObjectBucketCORS{}, err
	}
	r, err := sqlc.New().ObjectBucketCORSPut(ctx, s.pool, sqlc.ObjectBucketCORSPutParams{BucketID: mustPgUUID(bucket), Rules: raw})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.ObjectBucketCORS{}, ErrConflict
	}
	if err != nil {
		return api.ObjectBucketCORS{}, mapErr(err)
	}
	return api.ObjectBucketCORS{BucketID: bucket, Rules: cloneObjectBucketCORSRules(rules), Revision: r.Revision, UpdatedAt: r.UpdatedAt.Time.UTC()}, nil
}

func (s *pgCore) DeleteObjectBucketCORS(ctx context.Context, account, app, bucket string) error {
	if _, err := s.GetObjectBucket(ctx, account, app, bucket); err != nil {
		return err
	}
	return mapErr(sqlc.New().ObjectBucketCORSDelete(ctx, s.pool, mustPgUUID(bucket)))
}

func (s *pgCore) ObjectBucketCORSRules(ctx context.Context, bucket string) ([]api.ObjectBucketCORSRule, error) {
	raw, err := sqlc.New().ObjectBucketCORSRules(ctx, s.pool, mustPgUUID(bucket))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapErr(err)
	}
	return decodeObjectBucketCORSRules(raw)
}
