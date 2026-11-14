package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectStorageOverCeiling is a billing subject (an account, or an org that
// owns its resources) storing more object-storage bytes than its ceiling
// (ADR-20261111123000). OverCeilingSince and DeletionScheduledAt are fixed
// when the subject is first seen over; StoredBytes, CeilingBytes and
// ObservedAt follow the latest sweep.
type ObjectStorageOverCeiling struct {
	SubjectID           string
	OverCeilingSince    time.Time
	DeletionScheduledAt time.Time
	StoredBytes         int64
	CeilingBytes        int64
	ObservedAt          time.Time
	NoticesSent         int16
}

// ObjectStorageOverCeilingStore keeps the over-ceiling clocks. apid's sweep
// is the only writer.
type ObjectStorageOverCeilingStore interface {
	// GetObjectStorageOverCeiling returns ErrNotFound for a subject within
	// its ceiling.
	GetObjectStorageOverCeiling(ctx context.Context, subjectID string) (ObjectStorageOverCeiling, error)
	// ObserveObjectStorageOverCeiling records that the subject stores stored
	// bytes above ceiling at now. The first observation starts the clock and
	// schedules deletion api.ObjectStorageOverCeilingGraceDays later; later
	// ones keep it.
	ObserveObjectStorageOverCeiling(ctx context.Context, subjectID string, stored, ceiling int64, now time.Time) (ObjectStorageOverCeiling, error)
	// ClearObjectStorageOverCeiling stops the subject's clock, reporting
	// whether it had one.
	ClearObjectStorageOverCeiling(ctx context.Context, subjectID string) (bool, error)
}

var (
	_ ObjectStorageOverCeilingStore = (*MemStore)(nil)
	_ ObjectStorageOverCeilingStore = (*PgStore)(nil)
)

// Started reports whether the observation at now started the clock.
func (o ObjectStorageOverCeiling) Started(now time.Time) bool {
	return o.OverCeilingSince.Equal(now.UTC())
}

func objectStorageOverCeilingDeadline(now time.Time) time.Time {
	return now.UTC().AddDate(0, 0, api.ObjectStorageOverCeilingGraceDays)
}

func validObjectStorageOverCeilingObservation(stored, ceiling int64, now time.Time) error {
	if stored <= 0 || ceiling < 0 || ceiling >= stored || now.IsZero() {
		return fmt.Errorf("object storage over ceiling: stored %d is not above ceiling %d", stored, ceiling)
	}
	return nil
}

func objectStorageOverCeilingFromRow(r sqlc.ObjectStorageOverCeiling) ObjectStorageOverCeiling {
	return ObjectStorageOverCeiling{SubjectID: pgUUIDString(r.SubjectID), OverCeilingSince: r.OverCeilingSince.Time.UTC(),
		DeletionScheduledAt: r.DeletionScheduledAt.Time.UTC(), StoredBytes: r.StoredBytes, CeilingBytes: r.CeilingBytes,
		ObservedAt: r.ObservedAt.Time.UTC(), NoticesSent: r.NoticesSent}
}

// GetObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (s *PgStore) GetObjectStorageOverCeiling(ctx context.Context, subjectID string) (ObjectStorageOverCeiling, error) {
	subject, err := parsePgUUID(subjectID)
	if err != nil {
		return ObjectStorageOverCeiling{}, ErrNotFound
	}
	row, err := sqlc.New().GetObjectStorageOverCeiling(ctx, s.pool, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return ObjectStorageOverCeiling{}, ErrNotFound
	}
	if err != nil {
		return ObjectStorageOverCeiling{}, fmt.Errorf("read object storage over-ceiling clock: %w", err)
	}
	return objectStorageOverCeilingFromRow(row), nil
}

// ObserveObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (s *PgStore) ObserveObjectStorageOverCeiling(ctx context.Context, subjectID string, stored, ceiling int64, now time.Time) (ObjectStorageOverCeiling, error) {
	if err := validObjectStorageOverCeilingObservation(stored, ceiling, now); err != nil {
		return ObjectStorageOverCeiling{}, err
	}
	subject, err := parsePgUUID(subjectID)
	if err != nil {
		return ObjectStorageOverCeiling{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	row, err := sqlc.New().ObserveObjectStorageOverCeiling(ctx, s.pool, sqlc.ObserveObjectStorageOverCeilingParams{
		SubjectID: subject, ObservedAt: pgtype.Timestamptz{Time: now, Valid: true},
		DeletionScheduledAt: pgtype.Timestamptz{Time: objectStorageOverCeilingDeadline(now), Valid: true},
		StoredBytes:         stored, CeilingBytes: ceiling,
	})
	if err != nil {
		return ObjectStorageOverCeiling{}, fmt.Errorf("record object storage over-ceiling clock: %w", err)
	}
	return objectStorageOverCeilingFromRow(row), nil
}

// ClearObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (s *PgStore) ClearObjectStorageOverCeiling(ctx context.Context, subjectID string) (bool, error) {
	subject, err := parsePgUUID(subjectID)
	if err != nil {
		return false, err
	}
	n, err := sqlc.New().ClearObjectStorageOverCeiling(ctx, s.pool, subject)
	if err != nil {
		return false, fmt.Errorf("clear object storage over-ceiling clock: %w", err)
	}
	return n > 0, nil
}

// GetObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (m *MemStore) GetObjectStorageOverCeiling(_ context.Context, subjectID string) (ObjectStorageOverCeiling, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.objectStorageOverCeiling[subjectID]
	if !ok {
		return ObjectStorageOverCeiling{}, ErrNotFound
	}
	return o, nil
}

// ObserveObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (m *MemStore) ObserveObjectStorageOverCeiling(_ context.Context, subjectID string, stored, ceiling int64, now time.Time) (ObjectStorageOverCeiling, error) {
	if err := validObjectStorageOverCeilingObservation(stored, ceiling, now); err != nil {
		return ObjectStorageOverCeiling{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.objectStorageOverCeiling == nil {
		m.objectStorageOverCeiling = map[string]ObjectStorageOverCeiling{}
	}
	o, ok := m.objectStorageOverCeiling[subjectID]
	if !ok {
		o = ObjectStorageOverCeiling{SubjectID: subjectID, OverCeilingSince: now, DeletionScheduledAt: objectStorageOverCeilingDeadline(now)}
	}
	o.StoredBytes, o.CeilingBytes = stored, ceiling
	if now.After(o.ObservedAt) {
		o.ObservedAt = now
	}
	m.objectStorageOverCeiling[subjectID] = o
	return o, nil
}

// ClearObjectStorageOverCeiling implements ObjectStorageOverCeilingStore.
func (m *MemStore) ClearObjectStorageOverCeiling(_ context.Context, subjectID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objectStorageOverCeiling[subjectID]
	delete(m.objectStorageOverCeiling, subjectID)
	return ok, nil
}
