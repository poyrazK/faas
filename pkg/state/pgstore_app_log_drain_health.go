package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) UpsertAppLogDrainHealth(ctx context.Context, health AppLogDrainHealth) error {
	sampleAt := health.UpdatedAt.UTC()
	if sampleAt.IsZero() {
		sampleAt = time.Now().UTC()
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("state: begin app log drain health: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck
	_, err = tx.Exec(ctx, `
		INSERT INTO app_log_drain_health (
			drain_id, status, active, queue_depth, queue_capacity,
			pending_records, pending_bytes, pending_bytes_capacity, dead_letter_total,
			oldest_pending_at,
			delivered_total, failed_total, dropped_total, retries_total,
			delivery_latency_nanos_total, delivery_latency_samples,
			stream_reconnects_total, gaps_total, last_success_at,
			last_failure_at, last_error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)
		ON CONFLICT (drain_id) DO UPDATE SET
			status = EXCLUDED.status,
			active = EXCLUDED.active,
			queue_depth = EXCLUDED.queue_depth,
			queue_capacity = EXCLUDED.queue_capacity,
			pending_records = EXCLUDED.pending_records,
			pending_bytes = EXCLUDED.pending_bytes,
			pending_bytes_capacity = EXCLUDED.pending_bytes_capacity,
			dead_letter_total = EXCLUDED.dead_letter_total,
			oldest_pending_at = EXCLUDED.oldest_pending_at,
			delivered_total = EXCLUDED.delivered_total,
			failed_total = EXCLUDED.failed_total,
			dropped_total = EXCLUDED.dropped_total,
			retries_total = EXCLUDED.retries_total,
			delivery_latency_nanos_total = EXCLUDED.delivery_latency_nanos_total,
			delivery_latency_samples = EXCLUDED.delivery_latency_samples,
			stream_reconnects_total = EXCLUDED.stream_reconnects_total,
			gaps_total = EXCLUDED.gaps_total,
			last_success_at = EXCLUDED.last_success_at,
			last_failure_at = EXCLUDED.last_failure_at,
			last_error = EXCLUDED.last_error,
			updated_at = now()
	`, health.DrainID, healthStatus(health.Status), health.Active,
		health.QueueDepth, health.QueueCapacity, health.PendingRecords,
		health.PendingBytes, health.PendingBytesCapacity, health.DeadLetterTotal,
		nullableTime(health.OldestPendingAt), health.DeliveredTotal,
		health.FailedTotal, health.DroppedTotal, health.RetriesTotal,
		health.DeliveryLatencyNanosTotal, health.DeliveryLatencySamples,
		health.StreamReconnectsTotal, health.GapsTotal,
		nullableTime(health.LastSuccessAt), nullableTime(health.LastFailureAt),
		nullableStr(health.LastError))
	if err != nil {
		return fmt.Errorf("state: upsert app log drain health: %w", err)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO app_log_drain_delivery_analytics (
			drain_id, bucket_start, delivered_total, failed_total, dropped_total,
			retries_total, dead_letter_total, delivery_latency_nanos_total,
			delivery_latency_samples, pending_records, pending_bytes, sampled_at
		) VALUES ($1, date_trunc('hour', $2::timestamptz), $3, $4, $5, $6, $7, $8, $9, $10, $11, $2)
		ON CONFLICT (drain_id, bucket_start) DO UPDATE SET
			delivered_total = EXCLUDED.delivered_total,
			failed_total = EXCLUDED.failed_total,
			dropped_total = EXCLUDED.dropped_total,
			retries_total = EXCLUDED.retries_total,
			dead_letter_total = EXCLUDED.dead_letter_total,
			delivery_latency_nanos_total = EXCLUDED.delivery_latency_nanos_total,
			delivery_latency_samples = EXCLUDED.delivery_latency_samples,
			pending_records = EXCLUDED.pending_records,
			pending_bytes = EXCLUDED.pending_bytes,
			sampled_at = EXCLUDED.sampled_at
	`, health.DrainID, sampleAt, health.DeliveredTotal, health.FailedTotal,
		health.DroppedTotal, health.RetriesTotal, health.DeadLetterTotal,
		health.DeliveryLatencyNanosTotal, health.DeliveryLatencySamples,
		health.PendingRecords, health.PendingBytes)
	if err != nil {
		return fmt.Errorf("state: upsert app log drain delivery analytics: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit app log drain health: %w", err)
	}
	return nil
}

func (s *PgStore) AppLogDrainHealthByDrainID(ctx context.Context, drainID string) (AppLogDrainHealth, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT drain_id, status, active, queue_depth, queue_capacity,
		       pending_records, pending_bytes, pending_bytes_capacity, dead_letter_total,
		       oldest_pending_at,
		       delivered_total, failed_total, dropped_total, retries_total,
		       delivery_latency_nanos_total, delivery_latency_samples,
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
		       h.pending_records, h.pending_bytes, h.pending_bytes_capacity, h.dead_letter_total,
		       h.oldest_pending_at,
		       h.delivered_total, h.failed_total, h.dropped_total, h.retries_total,
		       h.delivery_latency_nanos_total, h.delivery_latency_samples,
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

func (s *PgStore) ListAppLogDrainDeliveryAnalytics(ctx context.Context, drainID string, from, to time.Time) ([]AppLogDrainDeliveryAnalytics, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT drain_id, bucket_start, delivered_total, failed_total, dropped_total,
		       retries_total, dead_letter_total, delivery_latency_nanos_total,
		       delivery_latency_samples, pending_records, pending_bytes, sampled_at
		FROM app_log_drain_delivery_analytics
		WHERE drain_id = $1 AND bucket_start >= $2 AND bucket_start <= $3
		ORDER BY bucket_start
	`, drainID, from, to)
	if err != nil {
		return nil, fmt.Errorf("state: list app log drain delivery analytics: %w", err)
	}
	defer rows.Close()
	out := make([]AppLogDrainDeliveryAnalytics, 0)
	for rows.Next() {
		var sample AppLogDrainDeliveryAnalytics
		if err := rows.Scan(&sample.DrainID, &sample.BucketStart, &sample.DeliveredTotal,
			&sample.FailedTotal, &sample.DroppedTotal, &sample.RetriesTotal,
			&sample.DeadLetterTotal, &sample.DeliveryLatencyNanosTotal,
			&sample.DeliveryLatencySamples, &sample.PendingRecords, &sample.PendingBytes,
			&sample.SampledAt); err != nil {
			return nil, fmt.Errorf("state: scan app log drain delivery analytics: %w", err)
		}
		sample.BucketStart = sample.BucketStart.UTC()
		sample.SampledAt = sample.SampledAt.UTC()
		out = append(out, sample)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list app log drain delivery analytics rows: %w", err)
	}
	return out, nil
}

func (s *PgStore) PruneAppLogDrainDeliveryAnalytics(ctx context.Context, before time.Time) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM app_log_drain_delivery_analytics WHERE bucket_start < $1`, before); err != nil {
		return fmt.Errorf("state: prune app log drain delivery analytics: %w", err)
	}
	return nil
}

type appLogDrainHealthRow interface {
	Scan(dest ...any) error
}

func scanAppLogDrainHealth(row appLogDrainHealthRow) (AppLogDrainHealth, error) {
	var health AppLogDrainHealth
	var oldestPendingAt, lastSuccessAt, lastFailureAt *time.Time
	if err := row.Scan(
		&health.DrainID, &health.Status, &health.Active, &health.QueueDepth,
		&health.QueueCapacity, &health.PendingRecords, &health.PendingBytes,
		&health.PendingBytesCapacity, &health.DeadLetterTotal, &oldestPendingAt,
		&health.DeliveredTotal, &health.FailedTotal,
		&health.DroppedTotal, &health.RetriesTotal,
		&health.DeliveryLatencyNanosTotal, &health.DeliveryLatencySamples,
		&health.StreamReconnectsTotal,
		&health.GapsTotal, &lastSuccessAt, &lastFailureAt, &health.LastError,
		&health.UpdatedAt,
	); err != nil {
		return AppLogDrainHealth{}, err
	}
	if lastSuccessAt != nil {
		health.LastSuccessAt = lastSuccessAt.UTC()
	}
	if oldestPendingAt != nil {
		health.OldestPendingAt = oldestPendingAt.UTC()
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
