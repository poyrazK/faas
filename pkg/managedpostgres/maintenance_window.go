package managedpostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbmaintenance"
)

func maintenanceEligible(d Database) bool {
	return !d.Spec.ScaleToZero && d.Spec.Availability == AvailabilitySingleZone
}

func (s *Service) SetMaintenanceWindow(ctx context.Context, accountID, id string, window *api.ManagedPostgresMaintenanceWindow) (api.ManagedPostgresMaintenanceStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok {
		return api.ManagedPostgresMaintenanceStatus{}, ErrUnsupported
	}
	d, err := customerDatabase(ctx, s.store, accountID, id)
	if err != nil {
		return api.ManagedPostgresMaintenanceStatus{}, err
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return api.ManagedPostgresMaintenanceStatus{}, err
	}
	if backend.Driver != "firecracker" || window != nil && !maintenanceEligible(d) {
		return api.ManagedPostgresMaintenanceStatus{}, ErrUnsupported
	}
	if d.ProviderResourceID == "" {
		return api.ManagedPostgresMaintenanceStatus{}, ErrConflict
	}
	if window != nil && !window.Valid() {
		return api.ManagedPostgresMaintenanceStatus{}, fmt.Errorf("%w: weekday must be 0–6, start_minute 0–1439, and duration_minutes 1–1440", ErrInvalid)
	}
	var result api.ManagedPostgresMaintenanceStatus
	err = pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		var eligible bool
		err := tx.QueryRow(ctx, `
			SELECT v.always_on AND d.state = 'ready' AND d.observed_generation = d.desired_generation
				  AND NOT d.scale_to_zero AND d.availability = 'single_zone'
			FROM database_vms v JOIN managed_postgres_databases d ON d.provider_resource_id = v.database_id::text
			WHERE d.id = $1::uuid AND v.database_id = $2::uuid
			FOR UPDATE OF v`, d.ID, d.ProviderResourceID).Scan(&eligible)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && window != nil && !eligible {
			return ErrConflict
		}
		if err != nil {
			return err
		}

		if window == nil {
			_, err = tx.Exec(ctx, `DELETE FROM native_postgres_maintenance_windows WHERE database_id = $1::uuid`, d.ProviderResourceID)
		} else {
			_, err = tx.Exec(ctx, `
				INSERT INTO native_postgres_maintenance_windows (database_id, weekday, start_minute, duration_minutes)
				VALUES ($1::uuid, $2, $3, $4)
				ON CONFLICT (database_id) DO UPDATE SET weekday = EXCLUDED.weekday,
					start_minute = EXCLUDED.start_minute, duration_minutes = EXCLUDED.duration_minutes`,
				d.ProviderResourceID, window.Weekday, window.StartMinute, window.DurationMinutes)
		}
		if err != nil {
			return err
		}
		result, err = s.readMaintenanceStatus(ctx, tx, d)
		return err
	})
	return result, mapPostgresError(err)
}

type maintenanceStatusReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// MaintenanceStatus reads only the authorized native database's stored schedule and latest refresh.
func (s *Service) MaintenanceStatus(ctx context.Context, d Database) (*api.ManagedPostgresMaintenanceStatus, error) {
	store, ok := s.store.(*PostgresStore)
	if !ok || d.ProviderResourceID == "" {
		return nil, nil
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return nil, err
	}
	if backend.Driver != "firecracker" {
		return nil, nil
	}
	status, err := s.readMaintenanceStatus(ctx, store.pool, d)
	if err != nil || (!maintenanceEligible(d) && status.Window == nil && status.LastRefresh == nil) {
		return nil, err
	}
	return &status, nil
}

func (s *Service) readMaintenanceStatus(ctx context.Context, db maintenanceStatusReader, d Database) (api.ManagedPostgresMaintenanceStatus, error) {
	var status api.ManagedPostgresMaintenanceStatus
	var weekday *int
	var window api.ManagedPostgresMaintenanceWindow
	var refresh api.ManagedPostgresBaseRefresh
	var startedAt *time.Time
	err := db.QueryRow(ctx, `
		SELECT p.weekday, COALESCE(p.start_minute, 0), COALESCE(p.duration_minutes, 0),
			p.stale_since, r.started_at,
			r.finished_at, COALESCE(NULLIF(r.outcome, ''), 'running'), COALESCE(r.phase, ''), COALESCE(r.reason, '')
		FROM database_vms v
		LEFT JOIN native_postgres_maintenance_windows p USING (database_id)
		LEFT JOIN LATERAL (
			SELECT started_at, finished_at, outcome, phase, reason FROM native_postgres_base_refreshes
			WHERE database_id = v.database_id AND refresh_base ORDER BY started_at DESC LIMIT 1
		) r ON TRUE WHERE v.database_id = $1::uuid`, d.ProviderResourceID).
		Scan(&weekday, &window.StartMinute, &window.DurationMinutes, &status.StaleSince, &startedAt,
			&refresh.FinishedAt, &refresh.Outcome, &refresh.Phase, &refresh.Reason)
	if errors.Is(err, pgx.ErrNoRows) {
		return status, nil
	}
	if err != nil {
		return status, err
	}
	if weekday != nil {
		window.Weekday = *weekday
		now := s.now()
		interval := dbmaintenance.Window(window).ActiveOrNext(now)
		status.Window, status.WindowStart, status.WindowEnd = &window, &interval.Start, &interval.End
		status.WindowOpen = interval.Contains(now)
	}
	if startedAt != nil {
		refresh.StartedAt = startedAt.UTC()
		if refresh.FinishedAt != nil {
			*refresh.FinishedAt = refresh.FinishedAt.UTC()
		}
		status.LastRefresh = &refresh
	}
	if status.StaleSince != nil {
		*status.StaleSince = status.StaleSince.UTC()
	}
	return status, nil
}
