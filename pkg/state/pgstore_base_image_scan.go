package state

// adr: 430

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BaseImageScanStore = (*PgStore)(nil)

func baseImageScanRow(row sqlc.BaseImageScan) (BaseImageScan, error) {
	var in BaseImageScanInput
	var result api.ScanResult
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return BaseImageScan{}, err
	}
	if err := json.Unmarshal(row.ResultSnapshot, &result); err != nil {
		return BaseImageScan{}, err
	}
	in.ID = pgUUIDString(row.ID)
	clock, err := time.Parse(time.RFC3339Nano, result.ScannedAt)
	if err != nil || !row.ScannedAt.Valid || !row.ExpiresAt.Valid || !clock.Equal(row.ScannedAt.Time) || in.BaseProducerID != pgUUIDString(row.BaseProducerID) || in.Artifact.StorageKey != row.StorageKey {
		return BaseImageScan{}, fmt.Errorf("base scan stored producer/clock mismatch")
	}
	result.ScannedAt = row.ScannedAt.Time.UTC().Format(time.RFC3339Nano)
	value := BaseImageScan{ID: in.ID, InputHash: row.InputHash, Input: in, ScannedAt: row.ScannedAt.Time, ExpiresAt: row.ExpiresAt.Time, Result: result}
	if err := validateBaseImageScan(value); err != nil {
		return BaseImageScan{}, err
	}
	return value, nil
}

func (s *PgStore) PublishBaseImageScan(ctx context.Context, input BaseImageScanInput) (BaseImageScan, error) {
	in, hash, err := prepareBaseImageScan(input)
	if err != nil {
		return BaseImageScan{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BaseImageScan{}, err
	}
	defer tx.Rollback(ctx)
	if err := lockBaseImageProducerKeys(ctx, tx, in.Artifact.StorageKey); err != nil {
		return BaseImageScan{}, err
	}
	q := sqlc.New()
	producer, err := q.GetCurrentBaseImageProducer(ctx, tx, in.Artifact.StorageKey)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && pgUUIDString(producer.ID) != in.BaseProducerID {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return BaseImageScan{}, err
	}
	base, err := baseImageProducerRow(producer)
	if err != nil {
		return BaseImageScan{}, err
	}
	if err := checkBaseScanProducer(in, base); err != nil {
		return BaseImageScan{}, err
	}
	existing, err := q.GetBaseImageScanByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		if existing.InputHash != hash {
			return BaseImageScan{}, ErrConflict
		}
		pointer, e := q.GetBaseImageScanPointer(ctx, tx, in.Artifact.StorageKey)
		if errors.Is(e, pgx.ErrNoRows) || e == nil && pgUUIDString(pointer) != in.ID {
			return BaseImageScan{}, ErrConflict
		}
		if e != nil {
			return BaseImageScan{}, e
		}
		value, e := baseImageScanRow(existing)
		if e != nil {
			return BaseImageScan{}, e
		}
		if e := tx.Commit(ctx); e != nil {
			return BaseImageScan{}, e
		}
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BaseImageScan{}, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return BaseImageScan{}, err
	}
	if err := q.AuthorizeBaseImageScanInsert(ctx, tx, mustPgUUID(in.ID)); err != nil {
		return BaseImageScan{}, err
	}
	row, err := q.InsertBaseImageScan(ctx, tx, sqlc.InsertBaseImageScanParams{ID: mustPgUUID(in.ID), InputSnapshot: raw, InputHash: hash, ProducerID: mustPgUUID(in.BaseProducerID), TtlSeconds: api.ApplicationStandardArtifactScanTTL.Seconds(), DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return BaseImageScan{}, err
	}
	value, err := baseImageScanRow(row)
	if err != nil {
		return BaseImageScan{}, err
	}
	if err := q.SelectBaseImageScan(ctx, tx, sqlc.SelectBaseImageScanParams{StorageKey: in.Artifact.StorageKey, ID: mustPgUUID(in.ID)}); err != nil {
		return BaseImageScan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BaseImageScan{}, err
	}
	return value, nil
}

func (s *PgStore) GetCurrentBaseImageScan(ctx context.Context, key string) (BaseImageScan, error) {
	if !imagechain.ValidBaseKey(key) {
		return BaseImageScan{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentBaseImageScan(ctx, s.pool, key)
	if err != nil {
		return BaseImageScan{}, registryVerificationError(err)
	}
	return baseImageScanRow(row)
}

func (s *PgStore) GetFreshBaseImageScan(ctx context.Context, id, hash string) (BaseImageScan, error) {
	if !validStandardResourceRead(id, id) || len(hash) != 64 {
		return BaseImageScan{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetFreshBaseImageScan(ctx, s.pool, sqlc.GetFreshBaseImageScanParams{ProducerID: mustPgUUID(id), ProducerHash: hash, DbMaxAgeSeconds: api.ApplicationStandardScannerDBMaxAge.Seconds()})
	if errors.Is(err, pgx.ErrNoRows) {
		return BaseImageScan{}, ErrApplicationStandardRuntimeStale
	}
	if err != nil {
		return BaseImageScan{}, err
	}
	return baseImageScanRow(row)
}
