package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// SLOWindowTotals sums an SLO's recorded hours since a cutoff (ADR-747).
type SLOWindowTotals struct {
	Good, Total int64
	Hours       int
}

// SLOBudgetStore holds the hourly good/total rows meterd records per SLO.
// Like SLOStore it is optional on Store; callers type-assert.
type SLOBudgetStore interface {
	ListAllSLOs(ctx context.Context) ([]SLO, error)
	SLOHourStarts(ctx context.Context, sloID string, since time.Time) ([]time.Time, error)
	UpsertSLOHour(ctx context.Context, sloID string, hour time.Time, good, total int64) error
	SumSLOHours(ctx context.Context, sloID string, since time.Time) (SLOWindowTotals, error)
	PurgeSLOHoursBefore(ctx context.Context, before time.Time) (int64, error)
}

func pgTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t.UTC(), Valid: true} }

func sloPgUUID(id string) (pgtype.UUID, error) {
	key, err := alertPgUUID(id)
	if err != nil || !key.Valid {
		return pgtype.UUID{}, ErrInvalidArgument
	}
	return key, nil
}

// ListAllSLOs returns every SLO across apps, for the meterd rollup.
func (s *PgStore) ListAllSLOs(ctx context.Context) ([]SLO, error) {
	rows, err := sqlc.New().ListAllAppSLOs(ctx, s.pool)
	if err != nil {
		return nil, fmt.Errorf("state: list all SLOs: %w", err)
	}
	out := make([]SLO, 0, len(rows))
	for _, row := range rows {
		out = append(out, sloFromRow(row))
	}
	return out, nil
}

// SLOHourStarts returns the hours already recorded for an SLO since a cutoff.
func (s *PgStore) SLOHourStarts(ctx context.Context, sloID string, since time.Time) ([]time.Time, error) {
	key, err := sloPgUUID(sloID)
	if err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListSLOHourStarts(ctx, s.pool, sqlc.ListSLOHourStartsParams{SloID: key, Since: pgTime(since)})
	if err != nil {
		return nil, fmt.Errorf("state: list SLO hours for %s: %w", sloID, err)
	}
	out := make([]time.Time, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Time.UTC())
	}
	return out, nil
}

// UpsertSLOHour records one hour's counts; re-recording an hour replaces it.
func (s *PgStore) UpsertSLOHour(ctx context.Context, sloID string, hour time.Time, good, total int64) error {
	key, err := sloPgUUID(sloID)
	if err != nil {
		return err
	}
	if err := sqlc.New().UpsertSLOHour(ctx, s.pool, sqlc.UpsertSLOHourParams{SloID: key, Hour: pgTime(hour), Good: good, Total: total}); err != nil {
		return fmt.Errorf("state: record SLO hour %s for %s: %w", hour.Format(time.RFC3339), sloID, mapErr(err))
	}
	return nil
}

// SumSLOHours totals an SLO's recorded hours since a cutoff.
func (s *PgStore) SumSLOHours(ctx context.Context, sloID string, since time.Time) (SLOWindowTotals, error) {
	key, err := sloPgUUID(sloID)
	if err != nil {
		return SLOWindowTotals{}, err
	}
	row, err := sqlc.New().SumSLOHours(ctx, s.pool, sqlc.SumSLOHoursParams{SloID: key, Since: pgTime(since)})
	if err != nil {
		return SLOWindowTotals{}, fmt.Errorf("state: sum SLO hours for %s: %w", sloID, err)
	}
	return SLOWindowTotals{Good: row.Good, Total: row.Total, Hours: int(row.Hours)}, nil
}

// PurgeSLOHoursBefore drops hours older than every window that could read them.
func (s *PgStore) PurgeSLOHoursBefore(ctx context.Context, before time.Time) (int64, error) {
	n, err := sqlc.New().PurgeSLOHoursBefore(ctx, s.pool, pgTime(before))
	if err != nil {
		return 0, fmt.Errorf("state: purge SLO hours: %w", err)
	}
	return n, nil
}

type memSLOHour struct{ good, total int64 }

func (m *MemStore) sloHoursLocked(sloID string) map[time.Time]memSLOHour {
	if m.sloHours == nil {
		m.sloHours = map[string]map[time.Time]memSLOHour{}
	}
	if m.sloHours[sloID] == nil {
		m.sloHours[sloID] = map[time.Time]memSLOHour{}
	}
	return m.sloHours[sloID]
}

// ListAllSLOs mirrors PgStore, ordered by id.
func (m *MemStore) ListAllSLOs(_ context.Context) ([]SLO, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []SLO
	for _, slos := range m.appSLOs {
		out = append(out, slos...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// SLOHourStarts mirrors PgStore.
func (m *MemStore) SLOHourStarts(_ context.Context, sloID string, since time.Time) ([]time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []time.Time
	for hour := range m.sloHoursLocked(sloID) {
		if !hour.Before(since) {
			out = append(out, hour)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out, nil
}

// UpsertSLOHour mirrors PgStore, including the foreign key and CHECKs.
func (m *MemStore) UpsertSLOHour(_ context.Context, sloID string, hour time.Time, good, total int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if good < 0 || total < good || !hour.Equal(hour.Truncate(time.Hour)) {
		return ErrInvalidArgument
	}
	found := false
	for _, slos := range m.appSLOs {
		for _, slo := range slos {
			found = found || slo.ID == sloID
		}
	}
	if !found {
		return ErrNotFound
	}
	m.sloHoursLocked(sloID)[hour.UTC()] = memSLOHour{good: good, total: total}
	return nil
}

// SumSLOHours mirrors PgStore.
func (m *MemStore) SumSLOHours(_ context.Context, sloID string, since time.Time) (SLOWindowTotals, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out SLOWindowTotals
	for hour, counts := range m.sloHoursLocked(sloID) {
		if !hour.Before(since) {
			out.Good += counts.good
			out.Total += counts.total
			out.Hours++
		}
	}
	return out, nil
}

// PurgeSLOHoursBefore mirrors PgStore.
func (m *MemStore) PurgeSLOHoursBefore(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for _, hours := range m.sloHours {
		for hour := range hours {
			if hour.Before(before) {
				delete(hours, hour)
				n++
			}
		}
	}
	return n, nil
}

var (
	_ SLOBudgetStore = (*PgStore)(nil)
	_ SLOBudgetStore = (*MemStore)(nil)
)
