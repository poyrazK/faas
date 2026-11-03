package managedpostgres

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Restore slot notices (ADR-20261031123259 delivery 2): the account that
// restored a copy is told once when the copy's slot is about to end without
// room, and once when the sweep scheduled its delete.

const (
	RestoreSlotNoticeEnding  = "ending"
	RestoreSlotNoticeExpired = "expired"
)

// RestoreSlotNotice is one recorded notice. RestorableUntil is set only for
// an expired copy: the end of its delete's undo window.
type RestoreSlotNotice struct {
	DatabaseID      string
	AccountID       string
	DatabaseName    string
	Kind            string
	SlotUntil       time.Time
	RestorableUntil time.Time
	RecordedAt      time.Time
}

// RestoreSlotNoticeStore is the optional store capability the slot sweep
// uses to record notices and find the ones still to mail.
type RestoreSlotNoticeStore interface {
	// RecordRestoreSlotNotice records a notice unless the copy already has
	// one of its kind. It reports whether it recorded it.
	RecordRestoreSlotNotice(ctx context.Context, notice RestoreSlotNotice) (bool, error)
	// UnmailedRestoreSlotNotices lists notices recorded after since that no
	// email has claimed, oldest first. An 'ending' notice is listed only
	// while its copy still holds a slot that has not ended at now.
	UnmailedRestoreSlotNotices(ctx context.Context, since, now time.Time, limit int) ([]RestoreSlotNotice, error)
}

func validRestoreSlotNotice(n RestoreSlotNotice) bool {
	switch n.Kind {
	case RestoreSlotNoticeEnding:
		if !n.RestorableUntil.IsZero() {
			return false
		}
	case RestoreSlotNoticeExpired:
		if n.RestorableUntil.IsZero() {
			return false
		}
	default:
		return false
	}
	return n.DatabaseID != "" && !n.SlotUntil.IsZero() && !n.RecordedAt.IsZero()
}

// RecordRestoreSlotNotice implements RestoreSlotNoticeStore.
func (s *PostgresStore) RecordRestoreSlotNotice(ctx context.Context, n RestoreSlotNotice) (bool, error) {
	id, err := postgresUUID(n.DatabaseID)
	if err != nil || !validRestoreSlotNotice(n) {
		return false, ErrInvalid
	}
	restorable := pgtype.Timestamptz{}
	if !n.RestorableUntil.IsZero() {
		restorable = databaseNullableTime(n.RestorableUntil)
	}
	recorded, err := new(sqlc.Queries).RecordManagedPostgresRestoreSlotNotice(ctx, s.catalog, sqlc.RecordManagedPostgresRestoreSlotNoticeParams{
		DatabaseID: id, Kind: n.Kind, SlotUntil: databaseNullableTime(n.SlotUntil), RestorableUntil: restorable,
		RecordedAt: databaseNullableTime(n.RecordedAt)})
	if err != nil {
		return false, mapPostgresError(err)
	}
	return recorded == 1, nil
}

// UnmailedRestoreSlotNotices implements RestoreSlotNoticeStore.
func (s *PostgresStore) UnmailedRestoreSlotNotices(ctx context.Context, since, now time.Time, limit int) ([]RestoreSlotNotice, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).ListUnmailedManagedPostgresRestoreSlotNotices(ctx, s.catalog, sqlc.ListUnmailedManagedPostgresRestoreSlotNoticesParams{
		Since: databaseNullableTime(since), Now: databaseNullableTime(now), BatchSize: int32(limit)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	notices := make([]RestoreSlotNotice, 0, len(rows))
	for _, row := range rows {
		n := RestoreSlotNotice{DatabaseID: databaseUUID(row.DatabaseID), AccountID: databaseUUID(row.AccountID), DatabaseName: row.Name,
			Kind: row.Kind, SlotUntil: row.SlotUntil.Time.UTC(), RecordedAt: row.RecordedAt.Time.UTC()}
		if row.RestorableUntil.Valid {
			n.RestorableUntil = row.RestorableUntil.Time.UTC()
		}
		notices = append(notices, n)
	}
	return notices, nil
}

type memoryRestoreSlotNoticeKey struct {
	database string
	kind     string
}

// RecordRestoreSlotNotice implements RestoreSlotNoticeStore.
func (s *MemoryStore) RecordRestoreSlotNotice(_ context.Context, n RestoreSlotNotice) (bool, error) {
	if !validRestoreSlotNotice(n) {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.databases[n.DatabaseID]; !ok {
		return false, ErrNotFound
	}
	key := memoryRestoreSlotNoticeKey{n.DatabaseID, n.Kind}
	if _, ok := s.restoreSlotNotices[key]; ok {
		return false, nil
	}
	s.restoreSlotNotices[key] = RestoreSlotNotice{DatabaseID: n.DatabaseID, Kind: n.Kind, SlotUntil: n.SlotUntil.UTC(),
		RestorableUntil: n.RestorableUntil.UTC(), RecordedAt: n.RecordedAt.UTC()}
	return true, nil
}

// UnmailedRestoreSlotNotices implements RestoreSlotNoticeStore.
func (s *MemoryStore) UnmailedRestoreSlotNotices(_ context.Context, since, now time.Time, limit int) ([]RestoreSlotNotice, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var notices []RestoreSlotNotice
	for key, n := range s.restoreSlotNotices {
		database, ok := s.databases[n.DatabaseID]
		if !ok || s.restoreSlotNoticesMailed[key] || !n.RecordedAt.After(since) {
			continue
		}
		if n.Kind == RestoreSlotNoticeEnding && (database.RestoreSlotUntil.IsZero() || !database.RestoreSlotUntil.After(now) ||
			database.State == StateDeleting || database.State == StateDeleted) {
			continue
		}
		n.AccountID, n.DatabaseName = database.AccountID, database.Name
		notices = append(notices, n)
	}
	sort.Slice(notices, func(i, j int) bool {
		a, b := notices[i], notices[j]
		if !a.RecordedAt.Equal(b.RecordedAt) {
			return a.RecordedAt.Before(b.RecordedAt)
		}
		if a.DatabaseID != b.DatabaseID {
			return a.DatabaseID < b.DatabaseID
		}
		return a.Kind < b.Kind
	})
	if len(notices) > limit {
		notices = notices[:limit]
	}
	return notices, nil
}

// markRestoreSlotNoticeMailed stands in for the mail outbox linking an intent.
func (s *MemoryStore) markRestoreSlotNoticeMailed(n RestoreSlotNotice) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restoreSlotNoticesMailed[memoryRestoreSlotNoticeKey{n.DatabaseID, n.Kind}] = true
}
