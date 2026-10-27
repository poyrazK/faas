package managedpostgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Compute overage (ADR-20261025094915 decision 2): a paid account may let its
// databases run past the plan's monthly compute allowance, up to twice it,
// before the cap suspends them. Off is the default; an account without a row
// is off. The setting is keyed by the usage subject: the organization when it
// owns resources, else the namespace, exactly as the ledger pools usage.

// OverageStore is an optional Store capability.
type OverageStore interface {
	OverageEnabled(ctx context.Context, subjectID string) (bool, error)
	SetOverage(ctx context.Context, subjectID string, enabled bool, now time.Time) error
}

// CapSubjectStore is an optional UsageStore capability: the subject the
// ledger pools each database's usage by. Without it, the enforcer pools by
// the database's account.
type CapSubjectStore interface {
	UsageSubjects(ctx context.Context, databaseIDs []string) (map[string]string, error)
}

func (s *PostgresStore) OverageEnabled(ctx context.Context, subjectID string) (bool, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil {
		return false, ErrInvalid
	}
	enabled, err := sqlc.New().ManagedPostgresOverageEnabled(ctx, s.catalog, subject)
	return enabled, mapPostgresError(err)
}

func (s *PostgresStore) SetOverage(ctx context.Context, subjectID string, enabled bool, now time.Time) error {
	subject, err := postgresUUID(subjectID)
	if err != nil || now.IsZero() {
		return ErrInvalid
	}
	return mapPostgresError(sqlc.New().SetManagedPostgresOverage(ctx, s.catalog, sqlc.SetManagedPostgresOverageParams{
		SubjectID: subject, Enabled: enabled, UpdatedAt: pgtype.Timestamptz{Time: now.UTC(), Valid: true},
	}))
}

func (s *PostgresStore) UsageSubjects(ctx context.Context, databaseIDs []string) (map[string]string, error) {
	ids := make([]pgtype.UUID, 0, len(databaseIDs))
	for _, id := range databaseIDs {
		parsed, err := postgresUUID(id)
		if err != nil {
			return nil, ErrInvalid
		}
		ids = append(ids, parsed)
	}
	rows, err := sqlc.New().ManagedPostgresUsageSubjects(ctx, s.catalog, ids)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	subjects := make(map[string]string, len(rows))
	for _, row := range rows {
		subjects[row.DatabaseID] = row.SubjectID
	}
	return subjects, nil
}

func (s *MemoryStore) OverageEnabled(_ context.Context, subjectID string) (bool, error) {
	if subjectID == "" {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.overage[subjectID], nil
}

func (s *MemoryStore) SetOverage(_ context.Context, subjectID string, enabled bool, now time.Time) error {
	if subjectID == "" || now.IsZero() {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.overage[subjectID] = enabled
	return nil
}

func (s *MemoryStore) UsageSubjects(_ context.Context, databaseIDs []string) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subjects := make(map[string]string, len(databaseIDs))
	for _, id := range databaseIDs {
		if database, ok := s.databases[id]; ok {
			subjects[id] = s.billingSubject(database)
		}
	}
	return subjects, nil
}

// Overage reports whether the subject's compute overage is on. A store
// without the capability reads as off.
func (s *Service) Overage(ctx context.Context, subjectID string) (bool, error) {
	store, ok := s.store.(OverageStore)
	if !ok {
		return false, nil
	}
	return store.OverageEnabled(ctx, subjectID)
}

// SetOverage records the subject's compute overage choice. Plan and
// capability checks belong to the caller.
func (s *Service) SetOverage(ctx context.Context, subjectID string, enabled bool) error {
	store, ok := s.store.(OverageStore)
	if !ok {
		return ErrUnsupported
	}
	return store.SetOverage(ctx, subjectID, enabled, s.now())
}
