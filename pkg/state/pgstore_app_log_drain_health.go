package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) UpsertAppLogDrainHealth(ctx context.Context, health AppLogDrainHealth) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO app_log_drain_health (
			drain_id, status, active, queue_depth, queue_capacity,
			delivered_total, failed_total, dropped_total, retries_total,
			stream_reconnects_total, gaps_total, last_success_at,
			last_failure_at, last_error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (drain_id) DO UPDATE SET
			status = EXCLUDED.status,
			active = EXCLUDED.active,
			queue_depth = EXCLUDED.queue_depth,
			queue_capacity = EXCLUDED.queue_capacity,
			delivered_total = EXCLUDED.delivered_total,
			failed_total = EXCLUDED.failed_total,
			dropped_total = EXCLUDED.dropped_total,
			retries_total = EXCLUDED.retries_total,
			stream_reconnects_total = EXCLUDED.stream_reconnects_total,
			gaps_total = EXCLUDED.gaps_total,
			last_success_at = EXCLUDED.last_success_at,
			last_failure_at = EXCLUDED.last_failure_at,
			last_error = EXCLUDED.last_error,
			updated_at = now()
	`, health.DrainID, healthStatus(health.Status), health.Active,
		health.QueueDepth, health.QueueCapacity, health.DeliveredTotal,
		health.FailedTotal, health.DroppedTotal, health.RetriesTotal,
		health.StreamReconnectsTotal, health.GapsTotal,
		nullableTime(health.LastSuccessAt), nullableTime(health.LastFailureAt),
		nullableStr(health.LastError))
	if err != nil {
		return fmt.Errorf("state: upsert app log drain health: %w", err)
	}
	return nil
}

func (s *PgStore) AppLogDrainHealthByDrainID(ctx context.Context, drainID string) (AppLogDrainHealth, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT drain_id, status, active, queue_depth, queue_capacity,
		       delivered_total, failed_total, dropped_total, retries_total,
		       stream_reconnects_total, gaps_total, last_success_at,
		       last_failure_at, coalesce(last_error, ''), updated_at
		FROM app_log_drain_health
		WHERE drain_id = $1
	`, drainID)
	health, err := scanAppLogDrainHealth(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppLogDrainHealth{}, ErrNotFound
		}
		return AppLogDrainHealth{}, fmt.Errorf("state: read app log drain health: %w", err)
	}
	return health, nil
}

func (s *PgStore) ListAppLogDrainHealthForApp(ctx context.Context, appID string) ([]AppLogDrainHealth, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT h.drain_id, h.status, h.active, h.queue_depth, h.queue_capacity,
		       h.delivered_total, h.failed_total, h.dropped_total, h.retries_total,
		       h.stream_reconnects_total, h.gaps_total, h.last_success_at,
		       h.last_failure_at, coalesce(h.last_error, ''), h.updated_at
		FROM app_log_drain_health h
		JOIN app_log_drains d ON d.id = h.drain_id
		WHERE d.app_id = $1
		ORDER BY h.drain_id
	`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list app log drain health: %w", err)
	}
	defer rows.Close()
	out := make([]AppLogDrainHealth, 0)
	for rows.Next() {
		health, scanErr := scanAppLogDrainHealth(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("state: scan app log drain health: %w", scanErr)
		}
		out = append(out, health)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list app log drain health rows: %w", err)
	}
	return out, nil
}

type appLogDrainHealthRow interface {
	Scan(dest ...any) error
}

func scanAppLogDrainHealth(row appLogDrainHealthRow) (AppLogDrainHealth, error) {
	var health AppLogDrainHealth
	var lastSuccessAt, lastFailureAt *time.Time
	if err := row.Scan(
		&health.DrainID, &health.Status, &health.Active, &health.QueueDepth,
		&health.QueueCapacity, &health.DeliveredTotal, &health.FailedTotal,
		&health.DroppedTotal, &health.RetriesTotal, &health.StreamReconnectsTotal,
		&health.GapsTotal, &lastSuccessAt, &lastFailureAt, &health.LastError,
		&health.UpdatedAt,
	); err != nil {
		return AppLogDrainHealth{}, err
	}
	if lastSuccessAt != nil {
		health.LastSuccessAt = lastSuccessAt.UTC()
	}
	if lastFailureAt != nil {
		health.LastFailureAt = lastFailureAt.UTC()
	}
	return health, nil
}

func healthStatus(status string) string {
	if status == "" {
		return "unknown"
	}
	return status
}
