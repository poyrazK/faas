package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/region"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) RuntimeLogArchiveDayExpired(ctx context.Context, instance, day string) (bool, error) {
	date, err := runtimeLogDay(day)
	if err != nil {
		return false, err
	}
	return sqlc.New().RuntimeLogArchiveDayExpired(ctx, s.pool, sqlc.RuntimeLogArchiveDayExpiredParams{InstanceID: instance, ArchiveDay: date})
}

func (s *PgStore) runtimeLogArchiveWriteMiss(ctx context.Context, instance, day string) error {
	if expired, err := s.RuntimeLogArchiveDayExpired(ctx, instance, day); err != nil {
		return err
	} else if expired {
		return ErrRuntimeLogArchiveExpired
	}
	return ErrNotFound
}

func (s *PgStore) ListRuntimeLogArchiveRetentionCandidates(ctx context.Context, q RuntimeLogArchiveRetentionQuery) ([]RuntimeLogArchive, error) {
	if !q.valid() {
		return nil, ErrInvalidArgument
	}
	before, _ := runtimeLogDay(q.BeforeDay)
	after, _ := runtimeLogDay(q.AfterDay)
	rows, err := sqlc.New().ListRuntimeLogArchiveRetentionCandidates(ctx, s.pool, sqlc.ListRuntimeLogArchiveRetentionCandidatesParams{BeforeDay: before, AfterDay: after, AfterInstance: q.AfterInstance, BatchSize: int32(q.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]RuntimeLogArchive, 0, len(rows))
	for _, r := range rows {
		out = append(out, runtimeLogArchiveFromPG(runtimeLogBindingFromPG(r.InstanceID, r.AppID, r.AccountID, r.DeploymentID, r.ComputeRegion), r.ArchiveDay, r.StorageKey, r.Bytes, r.Sha256, r.PublishedAt))
	}
	return out, nil
}

func (s *PgStore) RetireRuntimeLogArchive(ctx context.Context, ref RuntimeLogArchive) error {
	if !ref.ValidReference() || ref.Day == "9999-12-31" {
		return ErrInvalidArgument
	}
	if err := s.validateRuntimeLogArchiveOwner(ctx, ref); err != nil {
		return err
	}
	date, _ := runtimeLogDay(ref.Day)
	app, _ := parsePgUUID(ref.AppID)
	n, err := sqlc.New().RetireRuntimeLogArchive(ctx, s.pool, sqlc.RetireRuntimeLogArchiveParams{InstanceID: ref.InstanceID, ArchiveDay: date, StorageKey: ref.StorageKey, AppID: app})
	if err == nil && n != 1 {
		return ErrNotFound
	}
	return err
}

func (s *PgStore) validateRuntimeLogArchiveOwner(ctx context.Context, ref RuntimeLogArchive) error {
	binding, err := s.RuntimeLogArchiveBindingByInstance(ctx, ref.InstanceID)
	if err != nil {
		return err
	}
	if binding != ref.RuntimeLogArchiveBinding {
		return ErrInvalidArgument
	}
	return nil
}

func (s *PgStore) DeleteCollectedRuntimeLogArchive(ctx context.Context, ref RuntimeLogArchive) error {
	if !ref.ValidReference() {
		return ErrInvalidArgument
	}
	if err := s.validateRuntimeLogArchiveOwner(ctx, ref); err != nil {
		return err
	}
	date, _ := runtimeLogDay(ref.Day)
	app, _ := parsePgUUID(ref.AppID)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	q := sqlc.New()
	if err := q.RecordRuntimeLogArchiveCollection(ctx, tx, sqlc.RecordRuntimeLogArchiveCollectionParams{InstanceID: ref.InstanceID, ArchiveDay: date, StorageKey: ref.StorageKey, AppID: app}); err != nil {
		return err
	}
	n, err := q.DeleteCollectedRuntimeLogArchive(ctx, tx, sqlc.DeleteCollectedRuntimeLogArchiveParams{InstanceID: ref.InstanceID, ArchiveDay: date, StorageKey: ref.StorageKey, AppID: app})
	if err == nil && n != 1 {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ RuntimeLogArchiveRetentionStore = (*PgStore)(nil)
var _ RuntimeLogArchiveRetentionStore = (*MemStore)(nil)

func (s *PgStore) RuntimeLogArchiveDayCollected(ctx context.Context, instance, day string) (bool, error) {
	if !region.ValidRuntimeLogInstance(instance) {
		return false, ErrInvalidArgument
	}
	date, err := runtimeLogDay(day)
	if err != nil {
		return false, err
	}
	return sqlc.New().RuntimeLogArchiveDayCollected(ctx, s.pool, sqlc.RuntimeLogArchiveDayCollectedParams{InstanceID: instance, ArchiveDay: date})
}

var _ RuntimeLogArchiveCollectionReader = (*PgStore)(nil)
var _ RuntimeLogArchiveCollectionReader = (*MemStore)(nil)
