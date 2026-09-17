package state

import (
	"context"
	"fmt"
	"time"
)

// ControlPlaneChange is one durable broadcast event for a cache-backed
// control-plane resource. Every gateway replica keeps its own high-water
// cursor, so delivery is replayable without competing for a shared queue.
type ControlPlaneChange struct {
	ID           int64
	ResourceType string
	ResourceID   string
	AppID        string
	Operation    string
	CreatedAt    time.Time
}

// ControlPlaneChangeLogStore is intentionally additive to Store. It keeps
// the durable convergence loop available to production PgStore instances
// without forcing small in-memory test stores to implement a database seam.
type ControlPlaneChangeLogStore interface {
	LatestControlPlaneChangeID(context.Context) (int64, error)
	ListControlPlaneChangesAfter(context.Context, int64, int) ([]ControlPlaneChange, error)
	PruneControlPlaneChangeLog(context.Context, time.Time) (int64, error)
}

var _ ControlPlaneChangeLogStore = (*PgStore)(nil)

// LatestControlPlaneChangeID returns the durable high-water mark. Zero is a
// valid result when the ledger has not recorded a mutation yet.
func (s *PgStore) LatestControlPlaneChangeID(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: control-plane change log has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM control_plane_change_log`).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest control-plane change id: %w", err)
	}
	return id, nil
}

// ListControlPlaneChangesAfter reads the ledger in monotonic ID order. The
// store seam caps the batch so database-controlled history cannot turn a
// repair pass into an unbounded allocation.
func (s *PgStore) ListControlPlaneChangesAfter(ctx context.Context, afterID int64, limit int) ([]ControlPlaneChange, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: control-plane change log has nil pool")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, resource_type, resource_id, app_id, operation, created_at
		FROM control_plane_change_log
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list control-plane changes: %w", err)
	}
	defer rows.Close()
	changes := make([]ControlPlaneChange, 0, limit)
	for rows.Next() {
		var change ControlPlaneChange
		if err := rows.Scan(&change.ID, &change.ResourceType, &change.ResourceID, &change.AppID, &change.Operation, &change.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan control-plane change: %w", err)
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate control-plane changes: %w", err)
	}
	return changes, nil
}

// PruneControlPlaneChangeLog bounds retained history. A gateway that is
// already serving has its own cursor; a restarted gateway starts with an
// empty cache and baselines to the current high-water mark.
func (s *PgStore) PruneControlPlaneChangeLog(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: control-plane change log has nil pool")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("state: control-plane change log prune requires cutoff")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM control_plane_change_log WHERE created_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune control-plane change log: %w", err)
	}
	return tag.RowsAffected(), nil
}
