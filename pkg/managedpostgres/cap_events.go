package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Compute cap events (ADR-20261025094915 decision 8): the thresholds a usage
// subject reached in a UTC month, recorded once each.

func (s *PostgresStore) RecordCapEvent(ctx context.Context, event CapEvent) (bool, error) {
	subject, err := postgresUUID(event.SubjectID)
	if err != nil || !validCapEvent(event) {
		return false, ErrInvalid
	}
	_, err = sqlc.New().RecordManagedPostgresCapEvent(ctx, s.catalog, sqlc.RecordManagedPostgresCapEventParams{
		SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: event.PeriodStart.UTC(), Valid: true},
		Threshold: event.Threshold, ReachedAt: pgtype.Timestamptz{Time: event.ReachedAt.UTC(), Valid: true},
		UsedUnitSeconds: event.Used, AllowanceUnitSeconds: event.Allowance, CeilingUnitSeconds: event.Ceiling,
		OverageEnabled: event.Overage,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapPostgresError(err)
	}
	return true, nil
}

func (s *PostgresStore) CapSuspendedAt(ctx context.Context, subjectID string, periodStart time.Time) (*time.Time, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil || periodStart.IsZero() {
		return nil, ErrInvalid
	}
	reached, err := sqlc.New().ManagedPostgresCapSuspendedAt(ctx, s.catalog, sqlc.ManagedPostgresCapSuspendedAtParams{
		SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: monthStart(periodStart), Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapPostgresError(err)
	}
	at := reached.Time.UTC()
	return &at, nil
}

func (s *PostgresStore) MostSevereCapEvent(ctx context.Context, subjectID string, periodStart time.Time) (*CapEvent, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil || periodStart.IsZero() {
		return nil, ErrInvalid
	}
	month := monthStart(periodStart)
	row, err := sqlc.New().ManagedPostgresMostSevereCapEvent(ctx, s.catalog, sqlc.ManagedPostgresMostSevereCapEventParams{
		SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: month, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, mapPostgresError(err)
	}
	return &CapEvent{SubjectID: subjectID, PeriodStart: month, Threshold: row.Threshold, ReachedAt: row.ReachedAt.Time.UTC(),
		Used: row.UsedUnitSeconds, Allowance: row.AllowanceUnitSeconds, Ceiling: row.CeilingUnitSeconds,
		Overage: row.OverageEnabled, Mailed: row.Mailed}, nil
}

type memoryCapEventKey struct {
	subject   string
	period    time.Time
	threshold string
}

func (s *MemoryStore) RecordCapEvent(_ context.Context, event CapEvent) (bool, error) {
	if event.SubjectID == "" || !validCapEvent(event) {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryCapEventKey{event.SubjectID, event.PeriodStart.UTC(), event.Threshold}
	if _, ok := s.capEvents[key]; ok {
		return false, nil
	}
	s.capEvents[key] = event
	return true, nil
}

func (s *MemoryStore) CapSuspendedAt(_ context.Context, subjectID string, periodStart time.Time) (*time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	event, ok := s.capEvents[memoryCapEventKey{subjectID, monthStart(periodStart), CapThresholdSuspended}]
	if !ok {
		return nil, nil
	}
	at := event.ReachedAt.UTC()
	return &at, nil
}

// capThresholdOrder ranks thresholds from least to most severe.
var capThresholdOrder = []string{CapThresholdWarning, CapThresholdAllowance, CapThresholdCeilingWarning, CapThresholdSuspended}

func (s *MemoryStore) MostSevereCapEvent(_ context.Context, subjectID string, periodStart time.Time) (*CapEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, threshold := range slices.Backward(capThresholdOrder) {
		key := memoryCapEventKey{subjectID, monthStart(periodStart), threshold}
		if event, ok := s.capEvents[key]; ok {
			event.Mailed = s.capEventsMailed[key]
			return &event, nil
		}
	}
	return nil, nil
}

// markCapEventMailed stands in for the mail outbox linking an intent.
func (s *MemoryStore) markCapEventMailed(event CapEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.capEventsMailed[memoryCapEventKey{event.SubjectID, monthStart(event.PeriodStart), event.Threshold}] = true
}

func validCapEvent(event CapEvent) bool {
	switch event.Threshold {
	case CapThresholdWarning, CapThresholdAllowance, CapThresholdCeilingWarning, CapThresholdSuspended:
	default:
		return false
	}
	return !event.PeriodStart.IsZero() && event.PeriodStart.Equal(monthStart(event.PeriodStart)) &&
		!event.ReachedAt.Before(event.PeriodStart) && event.Used >= 0 && event.Allowance > 0 && event.Ceiling >= event.Allowance
}

// CapSuspendedAt reports when the cap suspended the subject's databases this
// month, or nil. A store without cap events reports nil.
func (s *Service) CapSuspendedAt(ctx context.Context, subjectID string, now time.Time) (*time.Time, error) {
	store, ok := s.store.(CapEventStore)
	if !ok {
		return nil, nil
	}
	return store.CapSuspendedAt(ctx, subjectID, now)
}
