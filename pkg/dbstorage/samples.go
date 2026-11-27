// Package dbstorage stores the last observed primary and standby volumes.
package dbstorage

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const FreshFor = 5 * time.Minute

type Sample struct {
	DatabaseID     string    `json:"database_id"`
	Role           string    `json:"role"`
	NodeID         string    `json:"node_id"`
	StateChangedAt time.Time `json:"state_changed_at"`
	AllocatedBytes int64     `json:"allocated_bytes"`
	TotalBytes     int64     `json:"total_bytes"`
	FreeBytes      int64     `json:"free_bytes"`
	Low            bool      `json:"low"`
	ReadOnly       bool      `json:"read_only"`
	SampledAt      time.Time `json:"sampled_at"`
}

// Record replaces an observation only if it still names the sampled running
// placement. One row per role keeps storage usage independent of sample rate.
func Record(ctx context.Context, pool *pgxpool.Pool, samples []Sample) error {
	_, err := pool.Exec(ctx, `
		INSERT INTO native_postgres_storage_samples AS previous (
			database_id, role, node_id, state_changed_at, allocated_bytes,
			total_bytes, free_bytes, low, read_only, sampled_at, low_since
		)
		SELECT s.database_id, s.role, s.node_id, s.state_changed_at, s.allocated_bytes,
			s.total_bytes, s.free_bytes, s.low, s.read_only, s.sampled_at,
			CASE WHEN s.low THEN s.sampled_at END
		FROM jsonb_to_recordset($1::jsonb) AS s(
			database_id uuid, role text, node_id text, state_changed_at timestamptz,
			allocated_bytes bigint, total_bytes bigint, free_bytes bigint,
			low boolean, read_only boolean, sampled_at timestamptz
		)
		JOIN database_vms v ON v.database_id = s.database_id
		WHERE v.state = 'running' AND NOT v.resize_pending
			AND v.state_changed_at = s.state_changed_at AND v.volume_bytes = s.allocated_bytes
			AND (s.role = 'primary' AND s.node_id = v.node_id
				OR s.role = 'standby' AND v.standby_running AND s.node_id = v.standby_node)
		ORDER BY s.database_id, s.role
		FOR SHARE OF v
		ON CONFLICT (database_id, role) DO UPDATE
		SET node_id = EXCLUDED.node_id, state_changed_at = EXCLUDED.state_changed_at,
			allocated_bytes = EXCLUDED.allocated_bytes, total_bytes = EXCLUDED.total_bytes,
			free_bytes = EXCLUDED.free_bytes, low = EXCLUDED.low,
			read_only = EXCLUDED.read_only, sampled_at = EXCLUDED.sampled_at,
			low_since = CASE
				WHEN EXCLUDED.low AND previous.low
					AND previous.state_changed_at = EXCLUDED.state_changed_at
					AND previous.node_id = EXCLUDED.node_id AND previous.allocated_bytes = EXCLUDED.allocated_bytes
					AND EXCLUDED.sampled_at - previous.sampled_at <= interval '5 minutes'
				THEN COALESCE(previous.low_since, previous.sampled_at)
				ELSE EXCLUDED.low_since END
		WHERE previous.sampled_at < EXCLUDED.sampled_at`, samples)
	return err
}

type Observation struct {
	Sample
	Fresh bool
}

// Read returns the last observations for the requested runtime IDs. Older
// observations, including from parked compute, are marked historical.
func Read(ctx context.Context, pool *pgxpool.Pool, ids []pgtype.UUID, now time.Time) ([]Observation, error) {
	rows, err := pool.Query(ctx, `
		SELECT v.database_id::text, s.role, s.allocated_bytes, s.total_bytes,
			s.free_bytes, s.low, s.read_only, s.sampled_at,
			(v.state = 'running' AND NOT v.resize_pending
				AND v.state_changed_at = s.state_changed_at AND v.volume_bytes = s.allocated_bytes
				AND (s.role = 'primary' OR v.standby_running)
				AND s.sampled_at >= $2) AS fresh
		FROM database_vms v
		JOIN native_postgres_storage_samples s ON s.database_id = v.database_id
			AND (s.role = 'primary' AND s.node_id = v.node_id
				OR s.role = 'standby' AND s.node_id = v.standby_node)
		WHERE v.database_id = ANY($1::uuid[])`, ids, now.Add(-FreshFor))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var observations []Observation
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.DatabaseID, &o.Role, &o.AllocatedBytes, &o.TotalBytes, &o.FreeBytes, &o.Low, &o.ReadOnly, &o.SampledAt, &o.Fresh); err != nil {
			return nil, err
		}
		observations = append(observations, o)
	}
	return observations, rows.Err()
}
