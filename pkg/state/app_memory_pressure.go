package state

// adr: 20261102124306 — per-app memory-pressure rollup for app health.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// AppMemoryPressure is an app's memory-pressure rollup: the guest reports
// (ADR-1155) since WindowStartedAt, which is at most
// api.AppHealthMemoryPressureWindow before the latest report.
type AppMemoryPressure struct {
	AppID             string
	WindowStartedAt   time.Time
	Episodes          int
	LowestAvailableMB int
	TotalMB           int
	PeakAnonMB        int
	LastAt            time.Time
}

// AppMemoryPressureStore keeps the rollup. schedd writes it from guest
// reports; apid reads it for app health (ADR-20261102124306).
type AppMemoryPressureStore interface {
	// RecordAppMemoryPressure adds one report at at. A report after the
	// window has run out starts a new window.
	RecordAppMemoryPressure(ctx context.Context, appID string, at time.Time, availableMB, totalMB, anonMB int) error
	// AppMemoryPressure returns the app's rollup; ok is false when it has none.
	AppMemoryPressure(ctx context.Context, appID string) (p AppMemoryPressure, ok bool, err error)
}

// nextMemoryPressure folds one report into the rollup, as the Postgres upsert
// does.
func nextMemoryPressure(old AppMemoryPressure, found bool, at time.Time, availableMB, totalMB, anonMB int) AppMemoryPressure {
	if !found || old.WindowStartedAt.Before(at.Add(-api.AppHealthMemoryPressureWindow)) {
		return AppMemoryPressure{AppID: old.AppID, WindowStartedAt: at, Episodes: 1, LowestAvailableMB: availableMB,
			TotalMB: totalMB, PeakAnonMB: anonMB, LastAt: at}
	}
	old.Episodes++
	old.LowestAvailableMB = min(old.LowestAvailableMB, availableMB)
	old.TotalMB = totalMB
	old.PeakAnonMB = max(old.PeakAnonMB, anonMB)
	if at.After(old.LastAt) {
		old.LastAt = at
	}
	return old
}

func validMemoryPressure(availableMB, totalMB, anonMB int) error {
	if availableMB < 0 || totalMB < 0 || anonMB < 0 {
		return fmt.Errorf("state: memory pressure values must not be negative: %w", ErrInvalidArgument)
	}
	return nil
}

// RecordAppMemoryPressure implements AppMemoryPressureStore.
func (s *PgStore) RecordAppMemoryPressure(ctx context.Context, appID string, at time.Time, availableMB, totalMB, anonMB int) error {
	if err := validMemoryPressure(availableMB, totalMB, anonMB); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		insert into app_memory_pressure as p
		       (app_id, window_started_at, episodes, lowest_available_mb, total_mb, peak_anon_mb, last_at)
		values ($1, $2, 1, $3, $4, $5, $2)
		on conflict (app_id) do update
		   set window_started_at   = case when p.window_started_at < $6 then $2 else p.window_started_at end,
		       episodes            = case when p.window_started_at < $6 then 1 else p.episodes + 1 end,
		       lowest_available_mb = case when p.window_started_at < $6 then $3 else least(p.lowest_available_mb, $3) end,
		       total_mb            = $4,
		       peak_anon_mb        = case when p.window_started_at < $6 then $5 else greatest(p.peak_anon_mb, $5) end,
		       last_at             = greatest(p.last_at, $2)
	`, appID, at, availableMB, totalMB, anonMB, at.Add(-api.AppHealthMemoryPressureWindow)); err != nil {
		return fmt.Errorf("state: record app %s memory pressure: %w", appID, err)
	}
	return nil
}

// AppMemoryPressure implements AppMemoryPressureStore.
func (s *PgStore) AppMemoryPressure(ctx context.Context, appID string) (AppMemoryPressure, bool, error) {
	p := AppMemoryPressure{AppID: appID}
	err := s.pool.QueryRow(ctx, `
		select window_started_at, episodes, lowest_available_mb, total_mb, peak_anon_mb, last_at
		  from app_memory_pressure
		 where app_id = $1`, appID).
		Scan(&p.WindowStartedAt, &p.Episodes, &p.LowestAvailableMB, &p.TotalMB, &p.PeakAnonMB, &p.LastAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppMemoryPressure{}, false, nil
	}
	if err != nil {
		return AppMemoryPressure{}, false, fmt.Errorf("state: read app %s memory pressure: %w", appID, err)
	}
	return p, true, nil
}

// RecordAppMemoryPressure mirrors PgStore.RecordAppMemoryPressure.
func (m *MemStore) RecordAppMemoryPressure(_ context.Context, appID string, at time.Time, availableMB, totalMB, anonMB int) error {
	if err := validMemoryPressure(availableMB, totalMB, anonMB); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apps[appID]; !ok {
		return fmt.Errorf("state: record app %s memory pressure: %w", appID, ErrNotFound)
	}
	if m.appMemoryPressure == nil {
		m.appMemoryPressure = map[string]AppMemoryPressure{}
	}
	old, found := m.appMemoryPressure[appID]
	old.AppID = appID
	m.appMemoryPressure[appID] = nextMemoryPressure(old, found, at, availableMB, totalMB, anonMB)
	return nil
}

// AppMemoryPressure mirrors PgStore.AppMemoryPressure.
func (m *MemStore) AppMemoryPressure(_ context.Context, appID string) (AppMemoryPressure, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.appMemoryPressure[appID]
	return p, ok, nil
}
