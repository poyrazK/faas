package managedpostgres

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Overage deliveries (ADR-20261031110037): the cumulative charge per billing
// provider, subject and UTC month.

func validOverageDelivery(d OverageDelivery) bool {
	return d.Provider != "" && !d.PeriodStart.IsZero() && d.PeriodStart.Equal(monthStart(d.PeriodStart)) &&
		d.BilledUnitSeconds >= 0 && d.Millicents > 0 && !d.UpdatedAt.Before(d.PeriodStart)
}

func (s *PostgresStore) OverageSubjects(ctx context.Context, periodStart time.Time) ([]OverageSubject, error) {
	if periodStart.IsZero() {
		return nil, ErrInvalid
	}
	rows, err := sqlc.New().ManagedPostgresOverageSubjects(ctx, s.catalog, pgtype.Timestamptz{Time: monthStart(periodStart), Valid: true})
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]OverageSubject, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverageSubject{SubjectID: postgresUUIDString(row.SubjectID), Allowance: row.AllowanceUnitSeconds, Overage: row.OverageEnabled})
	}
	return out, nil
}

func (s *PostgresStore) OverageDelivered(ctx context.Context, provider, subjectID string, periodStart time.Time) (OverageDelivery, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil || provider == "" || periodStart.IsZero() {
		return OverageDelivery{}, ErrInvalid
	}
	month := monthStart(periodStart)
	delivery := OverageDelivery{Provider: provider, SubjectID: subjectID, PeriodStart: month}
	row, err := sqlc.New().ManagedPostgresOverageDelivered(ctx, s.catalog, sqlc.ManagedPostgresOverageDeliveredParams{
		Provider: provider, SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: month, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return delivery, nil
	}
	if err != nil {
		return OverageDelivery{}, mapPostgresError(err)
	}
	delivery.Live, delivery.BilledUnitSeconds, delivery.Millicents, delivery.UpdatedAt = row.Live, row.BilledUnitSeconds, row.DeliveredMillicents, row.UpdatedAt.Time.UTC()
	return delivery, nil
}

func (s *PostgresStore) RecordOverageDelivery(ctx context.Context, delivery OverageDelivery, previousMillicents int64) (bool, error) {
	subject, err := postgresUUID(delivery.SubjectID)
	if err != nil || !validOverageDelivery(delivery) || previousMillicents < 0 || delivery.Millicents <= previousMillicents {
		return false, ErrInvalid
	}
	_, err = sqlc.New().RecordManagedPostgresOverageDelivery(ctx, s.catalog, sqlc.RecordManagedPostgresOverageDeliveryParams{
		Provider: delivery.Provider, SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: delivery.PeriodStart.UTC(), Valid: true},
		Live: delivery.Live, BilledUnitSeconds: delivery.BilledUnitSeconds, DeliveredMillicents: delivery.Millicents,
		UpdatedAt: pgtype.Timestamptz{Time: delivery.UpdatedAt.UTC(), Valid: true}, PreviousMillicents: previousMillicents,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapPostgresError(err)
	}
	return true, nil
}

func (s *PostgresStore) OveragePending(ctx context.Context, provider, subjectID string, periodStart time.Time) (OveragePendingStep, bool, error) {
	subject, err := postgresUUID(subjectID)
	if err != nil || provider == "" || periodStart.IsZero() {
		return OveragePendingStep{}, false, ErrInvalid
	}
	month := monthStart(periodStart)
	row, err := sqlc.New().ManagedPostgresOveragePending(ctx, s.catalog, sqlc.ManagedPostgresOveragePendingParams{
		Provider: provider, SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: month, Valid: true},
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return OveragePendingStep{}, false, nil
	}
	if err != nil {
		return OveragePendingStep{}, false, mapPostgresError(err)
	}
	return OveragePendingStep{Provider: provider, Live: row.Live, Step: OverageChargeStep{
		SubjectID: subjectID, PeriodStart: month, At: row.StepAt.Time.UTC(),
		UsedUnitSeconds: row.UsedUnitSeconds, AllowanceSeconds: row.AllowanceUnitSeconds, BilledUnitSeconds: row.BilledUnitSeconds,
		DeliveredMillicents: row.FromMillicents, TargetMillicents: row.TargetMillicents,
	}}, true, nil
}

func (s *PostgresStore) ReserveOverage(ctx context.Context, pending OveragePendingStep) (bool, error) {
	step := pending.Step
	subject, err := postgresUUID(step.SubjectID)
	if err != nil || !validOveragePending(pending) {
		return false, ErrInvalid
	}
	_, err = sqlc.New().ReserveManagedPostgresOverage(ctx, s.catalog, sqlc.ReserveManagedPostgresOverageParams{
		Provider: pending.Provider, SubjectID: subject, PeriodStart: pgtype.Timestamptz{Time: step.PeriodStart.UTC(), Valid: true},
		Live: pending.Live, StepAt: pgtype.Timestamptz{Time: step.At.UTC(), Valid: true},
		UsedUnitSeconds: step.UsedUnitSeconds, AllowanceUnitSeconds: step.AllowanceSeconds, BilledUnitSeconds: step.BilledUnitSeconds,
		FromMillicents: step.DeliveredMillicents, TargetMillicents: step.TargetMillicents,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, mapPostgresError(err)
	}
	return true, nil
}

// CompleteOverage records a reserved step as delivered and clears it in one
// transaction. When the month's charge no longer starts where the step does,
// the step is cleared without recording it.
func (s *PostgresStore) CompleteOverage(ctx context.Context, pending OveragePendingStep) (bool, error) {
	step := pending.Step
	subject, err := postgresUUID(step.SubjectID)
	if err != nil || !validOveragePending(pending) {
		return false, ErrInvalid
	}
	period := pgtype.Timestamptz{Time: step.PeriodStart.UTC(), Valid: true}
	recorded := false
	err = pgx.BeginFunc(ctx, s.catalog, func(tx pgx.Tx) error {
		q := sqlc.New()
		_, err := q.RecordManagedPostgresOverageDelivery(ctx, tx, sqlc.RecordManagedPostgresOverageDeliveryParams{
			Provider: pending.Provider, SubjectID: subject, PeriodStart: period,
			Live: pending.Live, BilledUnitSeconds: step.BilledUnitSeconds, DeliveredMillicents: step.TargetMillicents,
			UpdatedAt: pgtype.Timestamptz{Time: step.At.UTC(), Valid: true}, PreviousMillicents: step.DeliveredMillicents,
		})
		switch {
		case err == nil:
			recorded = true
		case !errors.Is(err, pgx.ErrNoRows):
			return err
		}
		return q.ClearManagedPostgresOveragePending(ctx, tx, sqlc.ClearManagedPostgresOveragePendingParams{
			Provider: pending.Provider, SubjectID: subject, PeriodStart: period, TargetMillicents: step.TargetMillicents,
		})
	})
	if err != nil {
		return false, mapPostgresError(err)
	}
	return recorded, nil
}

type memoryOverageKey struct {
	provider string
	subject  string
	period   time.Time
}

func (s *MemoryStore) OverageSubjects(_ context.Context, periodStart time.Time) ([]OverageSubject, error) {
	if periodStart.IsZero() {
		return nil, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []OverageSubject
	for key, event := range s.capEvents {
		if key.period.Equal(monthStart(periodStart)) && key.threshold == CapThresholdAllowance {
			out = append(out, OverageSubject{SubjectID: key.subject, Allowance: event.Allowance, Overage: event.Overage})
		}
	}
	slices.SortFunc(out, func(a, b OverageSubject) int { return strings.Compare(a.SubjectID, b.SubjectID) })
	return out, nil
}

func (s *MemoryStore) OverageDelivered(_ context.Context, provider, subjectID string, periodStart time.Time) (OverageDelivery, error) {
	if provider == "" || subjectID == "" || periodStart.IsZero() {
		return OverageDelivery{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	month := monthStart(periodStart)
	if delivery, ok := s.overageDeliveries[memoryOverageKey{provider, subjectID, month}]; ok {
		return delivery, nil
	}
	return OverageDelivery{Provider: provider, SubjectID: subjectID, PeriodStart: month}, nil
}

func (s *MemoryStore) RecordOverageDelivery(_ context.Context, delivery OverageDelivery, previousMillicents int64) (bool, error) {
	if delivery.SubjectID == "" || !validOverageDelivery(delivery) || previousMillicents < 0 || delivery.Millicents <= previousMillicents {
		return false, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryOverageKey{delivery.Provider, delivery.SubjectID, delivery.PeriodStart.UTC()}
	if s.overageDeliveries[key].Millicents != previousMillicents {
		return false, nil
	}
	delivery.PeriodStart, delivery.UpdatedAt = delivery.PeriodStart.UTC(), delivery.UpdatedAt.UTC()
	s.overageDeliveries[key] = delivery
	return true, nil
}
