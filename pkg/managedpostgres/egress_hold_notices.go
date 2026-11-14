package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Egress hold notices (ADR-20261110160000 amendment 2026-11-13): each
// database has its own monthly egress allowance and is held alone once it
// sends it, so the hold is recorded and mailed per database and month, naming
// the database. The event is a CapEvent with Threshold CapThresholdEgressHeld,
// DatabaseID set, and Used, Allowance and Ceiling in bytes.

// EgressHoldNoticeStore is an optional UsageStore capability.
// RecordEgressHoldNotice reports whether the notice is new; a repeat for the
// same database and month is a no-op. EgressHoldNotice reads one back, or
// nil.
type EgressHoldNoticeStore interface {
	RecordEgressHoldNotice(ctx context.Context, event CapEvent) (bool, error)
	EgressHoldNotice(ctx context.Context, databaseID string, periodStart time.Time) (*CapEvent, error)
}

func validEgressHoldNotice(event CapEvent) bool {
	return event.Threshold == CapThresholdEgressHeld && event.DatabaseID != "" && event.SubjectID != "" && validCapEvent(event)
}

// RecordEgressHoldNotice implements EgressHoldNoticeStore.
func (s *PostgresStore) RecordEgressHoldNotice(ctx context.Context, event CapEvent) (bool, error) {
	subject, subjectErr := postgresUUID(event.SubjectID)
	database, databaseErr := postgresUUID(event.DatabaseID)
	if subjectErr != nil || databaseErr != nil || !validEgressHoldNotice(event) {
		return false, ErrInvalid
	}
	_, err := sqlc.New().RecordManagedPostgresEgressHoldNotice(ctx, s.catalog, sqlc.RecordManagedPostgresEgressHoldNoticeParams{
		DatabaseID: database, PeriodStart: pgtype.Timestamptz{Time: event.PeriodStart.UTC(), Valid: true}, SubjectID: subject,
		ReachedAt: pgtype.Timestamptz{Time: event.ReachedAt.UTC(), Valid: true}, UsedBytes: event.Used,
		AllowanceBytes: event.Allowance, CeilingBytes: event.Ceiling, OverageBilled: event.Overage,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapPostgresError(err)
	}
	return true, nil
}

// EgressHoldNotice implements EgressHoldNoticeStore.
func (s *PostgresStore) EgressHoldNotice(ctx context.Context, databaseID string, periodStart time.Time) (*CapEvent, error) {
	database, err := postgresUUID(databaseID)
	if err != nil || periodStart.IsZero() {
		return nil, ErrInvalid
	}
	month := monthStart(periodStart)
	row, err := sqlc.New().ManagedPostgresEgressHoldNotice(ctx, s.catalog, sqlc.ManagedPostgresEgressHoldNoticeParams{
		DatabaseID: database, PeriodStart: pgtype.Timestamptz{Time: month, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapPostgresError(err)
	}
	return &CapEvent{SubjectID: postgresUUIDString(row.SubjectID), DatabaseID: databaseID, PeriodStart: month, Threshold: CapThresholdEgressHeld,
		ReachedAt: row.ReachedAt.Time.UTC(), Used: row.UsedBytes, Allowance: row.AllowanceBytes, Ceiling: row.CeilingBytes,
		Overage: row.OverageBilled, Mailed: row.Mailed}, nil
}

type memoryEgressHoldKey struct {
	database string
	period   time.Time
}

// RecordEgressHoldNotice implements EgressHoldNoticeStore.
func (s *MemoryStore) RecordEgressHoldNotice(_ context.Context, event CapEvent) (bool, error) {
	if !validEgressHoldNotice(event) {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryEgressHoldKey{event.DatabaseID, event.PeriodStart.UTC()}
	if _, ok := s.egressHolds[key]; ok {
		return false, nil
	}
	event.Mailed = false
	s.egressHolds[key] = event
	return true, nil
}

// EgressHoldNotice implements EgressHoldNoticeStore.
func (s *MemoryStore) EgressHoldNotice(_ context.Context, databaseID string, periodStart time.Time) (*CapEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.egressHolds[memoryEgressHoldKey{databaseID, monthStart(periodStart)}]
	if !ok {
		return nil, nil
	}
	return &event, nil
}
