package state

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ RuntimeSnapshotStore = (*PgStore)(nil)

func runtimeSnapshotFromSQL(row sqlc.RuntimeSnapshot) RuntimeSnapshotRecord {
	var retiredAt *time.Time
	if row.RetiredAt.Valid {
		value := row.RetiredAt.Time.UTC()
		retiredAt = &value
	}
	return RuntimeSnapshotRecord{
		ID:                  pgUUIDString(row.ID),
		CatalogKey:          row.CatalogKey,
		Runtime:             api.ExecutionRuntime(row.Runtime),
		Architecture:        row.Architecture,
		KernelDigest:        row.KernelDigest,
		GuestExecutorDigest: row.GuestExecutorDigest,
		BaseImageDigest:     row.BaseImageDigest,
		MemoryMB:            int(row.MemoryMb),
		EphemeralDiskMB:     int(row.EphemeralDiskMb),
		FormatVersion:       int(row.FormatVersion),
		StorageKey:          row.StorageKey,
		SnapshotDigest:      row.SnapshotDigest,
		MemBytes:            row.MemBytes,
		VMStateBytes:        row.VmStateBytes,
		Sanitized:           row.Sanitized,
		PayloadFree:         row.PayloadFree,
		State:               row.State,
		CreatedAt:           row.CreatedAt.Time.UTC(),
		PublishedAt:         row.PublishedAt.Time.UTC(),
		RetiredAt:           retiredAt,
	}
}

func runtimeSnapshotTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func runtimeSnapshotNullableTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return runtimeSnapshotTime(*value)
}

func (s *PgStore) PublishRuntimeSnapshot(ctx context.Context, record RuntimeSnapshotRecord) (RuntimeSnapshotRecord, error) {
	if record.State != RuntimeSnapshotStateReady {
		return RuntimeSnapshotRecord{}, ErrRuntimeSnapshotInvalid
	}
	if record.PublishedAt.IsZero() {
		record.PublishedAt = time.Now().UTC()
	} else {
		record.PublishedAt = record.PublishedAt.UTC()
	}
	record.CreatedAt = record.CreatedAt.UTC()
	if err := record.Validate(); err != nil {
		return RuntimeSnapshotRecord{}, err
	}
	row, err := sqlc.New().RuntimeSnapshotInsert(ctx, s.pool, sqlc.RuntimeSnapshotInsertParams{
		CatalogKey:          record.CatalogKey,
		Runtime:             string(record.Runtime),
		Architecture:        record.Architecture,
		KernelDigest:        record.KernelDigest,
		GuestExecutorDigest: record.GuestExecutorDigest,
		BaseImageDigest:     record.BaseImageDigest,
		MemoryMb:            int32(record.MemoryMB),
		EphemeralDiskMb:     int32(record.EphemeralDiskMB),
		FormatVersion:       int32(record.FormatVersion),
		StorageKey:          record.StorageKey,
		SnapshotDigest:      record.SnapshotDigest,
		MemBytes:            record.MemBytes,
		VmStateBytes:        record.VMStateBytes,
		Sanitized:           record.Sanitized,
		PayloadFree:         record.PayloadFree,
		State:               record.State,
		CreatedAt:           runtimeSnapshotTime(record.CreatedAt),
		PublishedAt:         runtimeSnapshotTime(record.PublishedAt),
		RetiredAt:           runtimeSnapshotNullableTime(record.RetiredAt),
	})
	if err != nil {
		return RuntimeSnapshotRecord{}, mapErr(err)
	}
	result := runtimeSnapshotFromSQL(row)
	if err := result.Validate(); err != nil {
		return RuntimeSnapshotRecord{}, fmt.Errorf("runtime snapshot insert returned invalid row: %w", err)
	}
	return result, nil
}

func (s *PgStore) LookupRuntimeSnapshot(ctx context.Context, catalogKey string) (RuntimeSnapshotRecord, error) {
	if catalogKey == "" {
		return RuntimeSnapshotRecord{}, ErrNotFound
	}
	row, err := sqlc.New().RuntimeSnapshotByCatalogKey(ctx, s.pool, catalogKey)
	if err != nil {
		return RuntimeSnapshotRecord{}, mapErr(err)
	}
	result := runtimeSnapshotFromSQL(row)
	if err := result.Validate(); err != nil {
		return RuntimeSnapshotRecord{}, fmt.Errorf("runtime snapshot lookup returned invalid row: %w", err)
	}
	return result, nil
}

func (s *PgStore) RetireRuntimeSnapshot(ctx context.Context, catalogKey string, retiredAt time.Time) error {
	if catalogKey == "" || retiredAt.IsZero() {
		return ErrRuntimeSnapshotInvalid
	}
	retiredAt = retiredAt.UTC()
	rows, err := sqlc.New().RuntimeSnapshotRetire(ctx, s.pool, sqlc.RuntimeSnapshotRetireParams{
		RetiredAt:  runtimeSnapshotTime(retiredAt),
		CatalogKey: catalogKey,
	})
	if err != nil {
		return mapErr(err)
	}
	if rows > 0 {
		return nil
	}
	// A second retirement is intentionally idempotent. A missing key remains
	// distinguishable so callers cannot mistake a publication race for success.
	record, err := s.LookupRuntimeSnapshot(ctx, catalogKey)
	if err != nil {
		return err
	}
	if record.State == RuntimeSnapshotStateRetired {
		return nil
	}
	return ErrConflict
}
