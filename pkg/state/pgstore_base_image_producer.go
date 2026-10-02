package state

// adr: 430

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/imagechain"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func baseImageProducerRow(row sqlc.BaseImageProducer) (BaseImageProducer, error) {
	var in BaseImageProducerInput
	if err := json.Unmarshal(row.InputSnapshot, &in); err != nil {
		return BaseImageProducer{}, err
	}
	in.ID = pgUUIDString(row.ID)
	value := BaseImageProducer{ID: in.ID, InputHash: row.InputHash, Input: in, PublishedAt: row.PublishedAt.Time}
	parent := ""
	if row.ParentProducerID.Valid {
		parent = pgUUIDString(row.ParentProducerID)
	}
	if !row.PublishedAt.Valid || in.Artifact.StorageKey != row.StorageKey || parent != in.ParentProducerID {
		return BaseImageProducer{}, fmt.Errorf("base producer stored owner mismatch")
	}
	if err := validateBaseImageProducer(value); err != nil {
		return BaseImageProducer{}, err
	}
	return value, nil
}
func lockBaseImageProducerKeys(ctx context.Context, tx pgx.Tx, keys ...string) error {
	slices.Sort(keys)
	for _, key := range slices.Compact(keys) {
		ok, err := sqlc.New().TryLockBaseImageProducerKey(ctx, tx, key)
		if err != nil {
			return err
		}
		if !ok {
			return ErrApplicationStandardReviewBusy
		}
	}
	return nil
}
func (s *PgStore) PublishBaseImageProducer(ctx context.Context, input BaseImageProducerInput) (BaseImageProducer, error) {
	in, hash, err := prepareBaseImageProducer(input)
	if err != nil {
		return BaseImageProducer{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return BaseImageProducer{}, err
	}
	defer tx.Rollback(ctx)
	q := sqlc.New()
	keys := []string{in.Artifact.StorageKey}
	var parent BaseImageProducer
	if in.ParentProducerID != "" {
		row, err := q.GetBaseImageProducerByID(ctx, tx, mustPgUUID(in.ParentProducerID))
		if err != nil {
			return BaseImageProducer{}, registryVerificationError(err)
		}
		parent, err = baseImageProducerRow(row)
		if err != nil {
			return BaseImageProducer{}, err
		}
		keys = append(keys, parent.Input.Artifact.StorageKey)
	}
	if err := lockBaseImageProducerKeys(ctx, tx, keys...); err != nil {
		return BaseImageProducer{}, err
	}
	row, err := q.GetBaseImageProducerByID(ctx, tx, mustPgUUID(in.ID))
	if err == nil {
		current, e := q.GetCurrentBaseImageProducer(ctx, tx, in.Artifact.StorageKey)
		if row.InputHash != hash || errors.Is(e, pgx.ErrNoRows) || e == nil && pgUUIDString(current.ID) != in.ID {
			return BaseImageProducer{}, ErrConflict
		}
		if e != nil {
			return BaseImageProducer{}, e
		}
		value, e := baseImageProducerRow(row)
		if e != nil {
			return BaseImageProducer{}, e
		}
		if e := tx.Commit(ctx); e != nil {
			return BaseImageProducer{}, e
		}
		return value, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return BaseImageProducer{}, err
	}
	if in.ParentProducerID != "" {
		current, e := q.GetCurrentBaseImageProducer(ctx, tx, parent.Input.Artifact.StorageKey)
		if errors.Is(e, pgx.ErrNoRows) || e == nil && pgUUIDString(current.ID) != parent.ID {
			return BaseImageProducer{}, ErrApplicationStandardRuntimeStale
		}
		if e != nil {
			return BaseImageProducer{}, e
		}
		if e := checkBaseImageProducerParent(in, parent); e != nil {
			return BaseImageProducer{}, e
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return BaseImageProducer{}, err
	}
	if err := q.AuthorizeBaseImageProducerInsert(ctx, tx, in.ID); err != nil {
		return BaseImageProducer{}, err
	}
	row, err = q.InsertBaseImageProducer(ctx, tx, sqlc.InsertBaseImageProducerParams{ID: mustPgUUID(in.ID), StorageKey: in.Artifact.StorageKey, InputSnapshot: raw, InputHash: hash})
	if err != nil {
		return BaseImageProducer{}, err
	}
	if err := q.SelectBaseImageProducer(ctx, tx, sqlc.SelectBaseImageProducerParams{StorageKey: in.Artifact.StorageKey, ProducerID: mustPgUUID(in.ID)}); err != nil {
		return BaseImageProducer{}, err
	}
	value, err := baseImageProducerRow(row)
	if err != nil {
		return BaseImageProducer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return BaseImageProducer{}, err
	}
	return value, nil
}
func (s *PgStore) GetCurrentBaseImageProducer(ctx context.Context, key string) (BaseImageProducer, error) {
	if !imagechain.ValidBaseKey(key) {
		return BaseImageProducer{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetCurrentBaseImageProducer(ctx, s.pool, key)
	if err != nil {
		return BaseImageProducer{}, registryVerificationError(err)
	}
	return baseImageProducerRow(row)
}
func (s *PgStore) GetBaseImageProducerByID(ctx context.Context, id string) (BaseImageProducer, error) {
	if !validStandardResourceRead(id, id) {
		return BaseImageProducer{}, ErrInvalidArgument
	}
	row, err := sqlc.New().GetBaseImageProducerByID(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return BaseImageProducer{}, registryVerificationError(err)
	}
	return baseImageProducerRow(row)
}
