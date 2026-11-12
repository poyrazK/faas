package managedpostgres

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Plan excess notices (ADR-20261102125208 step 3): the owner of a database
// the cap sweep suspended because its plan no longer includes it is told
// once. A database back inside the plan loses its notice, so a later
// suspension is told again.

// PlanExcessNotice is one suspended database's notice.
type PlanExcessNotice struct {
	DatabaseID   string
	AccountID    string
	DatabaseName string
	ServiceClass ServiceClass
	Reason       string
	RecordedAt   time.Time
	// DeleteAt is when the database is deleted if it still does not fit,
	// set only while the deletion pass is on (ADR-20261110184700).
	DeleteAt time.Time
	// Reminder marks the one reminder sent a week before DeleteAt
	// (ADR-20261110184700 amendment 2026-11-12); RecordedAt is then when it
	// was found due.
	Reminder bool
}

// PlanExcessReminderStore is the optional store capability the cap sweep uses
// to find the deletion reminders due.
type PlanExcessReminderStore interface {
	// PlanExcessRemindersDue lists notices whose mailed deletion date falls
	// after now and within api.ManagedPostgresPlanExcessDeleteReminder of
	// it, with no reminder queued, for databases not being deleted, soonest
	// first.
	PlanExcessRemindersDue(ctx context.Context, now time.Time, limit int) ([]PlanExcessNotice, error)
}

// PlanExcessNoticeStore is the optional store capability the cap sweep uses
// to record notices and find the ones still to mail.
type PlanExcessNoticeStore interface {
	// SyncPlanExcessNotices records a notice for each database in suspended
	// (database ID to reason) that has none, and removes the notices of the
	// subject's other databases. It reports how many it recorded.
	SyncPlanExcessNotices(ctx context.Context, databaseIDs []string, suspended map[string]string, recordedAt time.Time) (int, error)
	// UnmailedPlanExcessNotices lists notices recorded after since that no
	// email has claimed, oldest first, for databases not being deleted.
	UnmailedPlanExcessNotices(ctx context.Context, since time.Time, limit int) ([]PlanExcessNotice, error)
}

func validPlanExcessReason(reason string) bool {
	return reason == PlanExcessDatabaseLimit || reason == PlanExcessServiceClass
}

// SyncPlanExcessNotices implements PlanExcessNoticeStore.
func (s *PostgresStore) SyncPlanExcessNotices(ctx context.Context, databaseIDs []string, suspended map[string]string, recordedAt time.Time) (int, error) {
	if recordedAt.IsZero() {
		return 0, ErrInvalid
	}
	var inside []pgtype.UUID
	for _, id := range databaseIDs {
		if _, ok := suspended[id]; ok {
			continue
		}
		uid, err := postgresUUID(id)
		if err != nil {
			return 0, ErrInvalid
		}
		inside = append(inside, uid)
	}
	queries := new(sqlc.Queries)
	if len(inside) > 0 {
		if err := queries.ClearManagedPostgresPlanExcessNotices(ctx, s.catalog, inside); err != nil {
			return 0, mapPostgresError(err)
		}
	}
	recorded := 0
	for _, id := range sortedKeys(suspended) {
		uid, err := postgresUUID(id)
		if err != nil || !validPlanExcessReason(suspended[id]) {
			return recorded, ErrInvalid
		}
		n, err := queries.RecordManagedPostgresPlanExcessNotice(ctx, s.catalog, sqlc.RecordManagedPostgresPlanExcessNoticeParams{
			DatabaseID: uid, Reason: suspended[id], RecordedAt: databaseNullableTime(recordedAt)})
		if err != nil {
			return recorded, mapPostgresError(err)
		}
		recorded += int(n)
	}
	return recorded, nil
}

// UnmailedPlanExcessNotices implements PlanExcessNoticeStore.
func (s *PostgresStore) UnmailedPlanExcessNotices(ctx context.Context, since time.Time, limit int) ([]PlanExcessNotice, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).ListUnmailedManagedPostgresPlanExcessNotices(ctx, s.catalog, sqlc.ListUnmailedManagedPostgresPlanExcessNoticesParams{
		Since: databaseNullableTime(since), BatchSize: int32(limit)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	notices := make([]PlanExcessNotice, 0, len(rows))
	for _, row := range rows {
		notices = append(notices, PlanExcessNotice{DatabaseID: databaseUUID(row.DatabaseID), AccountID: databaseUUID(row.AccountID),
			DatabaseName: row.Name, ServiceClass: ServiceClass(row.ServiceClass), Reason: row.Reason, RecordedAt: row.RecordedAt.Time.UTC()})
	}
	return notices, nil
}

// PlanExcessRemindersDue implements PlanExcessReminderStore.
func (s *PostgresStore) PlanExcessRemindersDue(ctx context.Context, now time.Time, limit int) ([]PlanExcessNotice, error) {
	if limit < 1 || limit > 500 || now.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := new(sqlc.Queries).ListManagedPostgresPlanExcessRemindersDue(ctx, s.catalog, sqlc.ListManagedPostgresPlanExcessRemindersDueParams{
		Now: databaseNullableTime(now), RemindUntil: databaseNullableTime(now.Add(api.ManagedPostgresPlanExcessDeleteReminder)), BatchSize: int32(limit)})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	notices := make([]PlanExcessNotice, 0, len(rows))
	for _, row := range rows {
		notices = append(notices, PlanExcessNotice{DatabaseID: databaseUUID(row.DatabaseID), AccountID: databaseUUID(row.AccountID),
			DatabaseName: row.Name, ServiceClass: ServiceClass(row.ServiceClass), Reason: row.Reason, RecordedAt: now.UTC(),
			DeleteAt: row.DeleteAt.Time.UTC(), Reminder: true})
	}
	return notices, nil
}

// SyncPlanExcessNotices implements PlanExcessNoticeStore.
func (s *MemoryStore) SyncPlanExcessNotices(_ context.Context, databaseIDs []string, suspended map[string]string, recordedAt time.Time) (int, error) {
	if recordedAt.IsZero() {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range databaseIDs {
		if _, ok := suspended[id]; !ok {
			delete(s.planExcessNotices, id)
		}
	}
	recorded := 0
	for _, id := range sortedKeys(suspended) {
		if !validPlanExcessReason(suspended[id]) {
			return recorded, ErrInvalid
		}
		if _, ok := s.databases[id]; !ok {
			return recorded, ErrNotFound
		}
		if _, ok := s.planExcessNotices[id]; ok {
			continue
		}
		s.planExcessNotices[id] = memoryPlanExcessNotice{PlanExcessNotice: PlanExcessNotice{DatabaseID: id, Reason: suspended[id], RecordedAt: recordedAt.UTC()}}
		recorded++
	}
	return recorded, nil
}

// UnmailedPlanExcessNotices implements PlanExcessNoticeStore.
func (s *MemoryStore) UnmailedPlanExcessNotices(_ context.Context, since time.Time, limit int) ([]PlanExcessNotice, error) {
	if limit < 1 || limit > 500 {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var notices []PlanExcessNotice
	for id, n := range s.planExcessNotices {
		database, ok := s.databases[id]
		if !ok || n.mailed || !n.RecordedAt.After(since) || database.State == StateDeleting || database.State == StateDeleted {
			continue
		}
		n.AccountID, n.DatabaseName, n.ServiceClass = database.AccountID, database.Name, database.Spec.Class
		notices = append(notices, n.PlanExcessNotice)
	}
	sort.Slice(notices, func(i, j int) bool {
		if !notices[i].RecordedAt.Equal(notices[j].RecordedAt) {
			return notices[i].RecordedAt.Before(notices[j].RecordedAt)
		}
		return notices[i].DatabaseID < notices[j].DatabaseID
	})
	if len(notices) > limit {
		notices = notices[:limit]
	}
	return notices, nil
}

// PlanExcessRemindersDue implements PlanExcessReminderStore.
func (s *MemoryStore) PlanExcessRemindersDue(_ context.Context, now time.Time, limit int) ([]PlanExcessNotice, error) {
	if limit < 1 || limit > 500 || now.IsZero() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	until := now.Add(api.ManagedPostgresPlanExcessDeleteReminder)
	var notices []PlanExcessNotice
	for id, n := range s.planExcessNotices {
		database, ok := s.databases[id]
		if !ok || !n.mailed || n.reminded || n.deleteAt.IsZero() || !n.deleteAt.After(now) || n.deleteAt.After(until) ||
			database.State == StateDeleting || database.State == StateDeleted {
			continue
		}
		notice := n.PlanExcessNotice
		notice.AccountID, notice.DatabaseName, notice.ServiceClass = database.AccountID, database.Name, database.Spec.Class
		notice.RecordedAt, notice.DeleteAt, notice.Reminder = now.UTC(), n.deleteAt, true
		notices = append(notices, notice)
	}
	sort.Slice(notices, func(i, j int) bool {
		if !notices[i].DeleteAt.Equal(notices[j].DeleteAt) {
			return notices[i].DeleteAt.Before(notices[j].DeleteAt)
		}
		return notices[i].DatabaseID < notices[j].DatabaseID
	})
	if len(notices) > limit {
		notices = notices[:limit]
	}
	return notices, nil
}

// memoryPlanExcessNotice is a notice, whether an email claimed it, the
// deletion date that email named, and whether its reminder was queued.
type memoryPlanExcessNotice struct {
	PlanExcessNotice
	mailed   bool
	deleteAt time.Time
	reminded bool
}

// markPlanExcessNoticeReminded stands in for the mail outbox linking a
// reminder's intent.
func (s *MemoryStore) markPlanExcessNoticeReminded(databaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.planExcessNotices[databaseID]; ok {
		n.reminded = true
		s.planExcessNotices[databaseID] = n
	}
}

// markPlanExcessNoticeMailed stands in for the mail outbox linking an intent
// that names deleteAt (zero: no deletion date).
func (s *MemoryStore) markPlanExcessNoticeMailed(databaseID string, deleteAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.planExcessNotices[databaseID]; ok && !n.mailed {
		n.mailed, n.deleteAt = true, deleteAt.UTC()
		s.planExcessNotices[databaseID] = n
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
