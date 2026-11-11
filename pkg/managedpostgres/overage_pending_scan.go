package managedpostgres

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func validOveragePendingPage(provider string, after OveragePendingRef, through time.Time, limit int) bool {
	return provider != "" && !through.IsZero() && limit >= 1 && limit <= 500 &&
		((after.SubjectID == "" && after.PeriodStart.IsZero()) || (after.SubjectID != "" && !after.PeriodStart.IsZero() && after.PeriodStart.Equal(monthStart(after.PeriodStart))))
}

func (s *PostgresStore) PendingOverage(ctx context.Context, provider string, after OveragePendingRef, through time.Time, limit int) ([]OveragePendingRef, error) {
	if !validOveragePendingPage(provider, after, through, limit) {
		return nil, ErrInvalid
	}
	params := sqlc.ListManagedPostgresOveragePendingParams{Provider: provider, Through: databaseNullableTime(monthStart(through)),
		PageLimit: int32(limit), AfterPeriod: databaseNullableTime(after.PeriodStart)} //nolint:gosec // bounded page size
	if after.SubjectID != "" {
		subject, err := postgresUUID(after.SubjectID)
		if err != nil {
			return nil, err
		}
		params.AfterSubject = subject
	}
	rows, err := sqlc.New().ListManagedPostgresOveragePending(ctx, s.catalog, params)
	if err != nil {
		return nil, mapPostgresError(err)
	}
	out := make([]OveragePendingRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, OveragePendingRef{SubjectID: postgresUUIDString(row.SubjectID), PeriodStart: row.PeriodStart.Time.UTC()})
	}
	return out, nil
}

func (s *MemoryStore) PendingOverage(_ context.Context, provider string, after OveragePendingRef, through time.Time, limit int) ([]OveragePendingRef, error) {
	if !validOveragePendingPage(provider, after, through, limit) {
		return nil, ErrInvalid
	}
	through = monthStart(through)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]OveragePendingRef, 0)
	for key := range s.overagePending {
		ref := OveragePendingRef{SubjectID: key.subject, PeriodStart: key.period}
		if key.provider == provider && !key.period.After(through) && (after.SubjectID == "" || compareOverageRefs(ref, after) > 0) {
			out = append(out, ref)
		}
	}
	slices.SortFunc(out, compareOverageRefs)
	return out[:min(len(out), limit)], nil
}

func compareOverageRefs(a, b OveragePendingRef) int {
	if c := a.PeriodStart.Compare(b.PeriodStart); c != 0 {
		return c
	}
	return strings.Compare(a.SubjectID, b.SubjectID)
}
