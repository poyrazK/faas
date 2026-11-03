package managedpostgres

import (
	"context"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

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

// memoryPlanExcessNotice is a notice and whether an email claimed it.
type memoryPlanExcessNotice struct {
	PlanExcessNotice
	mailed bool
}

// markPlanExcessNoticeMailed stands in for the mail outbox linking an intent.
func (s *MemoryStore) markPlanExcessNoticeMailed(databaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n, ok := s.planExcessNotices[databaseID]; ok {
		n.mailed = true
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
