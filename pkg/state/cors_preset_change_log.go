package state

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CorsPresetChange is one durable mutation of an account-scoped shared CORS
// preset. IDs are global so each gateway can keep a single replay cursor.
type CorsPresetChange struct {
	ID        int64
	AccountID string
	PresetID  string
	Operation string
	CreatedAt time.Time
}

// CorsPresetChangeLogStore is additive to Store because only PostgreSQL
// gateway processes need the durable replay and fleet-status capabilities.
type CorsPresetChangeLogStore interface {
	LatestCorsPresetChangeID(context.Context) (int64, error)
	LatestAccountCorsPresetChangeID(context.Context, string) (int64, error)
	ListCorsPresetChangesAfter(context.Context, int64, int) ([]CorsPresetChange, error)
	PruneCorsPresetChangeLog(context.Context, time.Time) (int64, error)
	UpsertGatewayCorsPresetWatermark(context.Context, string, string, int64) error
}

var _ CorsPresetChangeLogStore = (*PgStore)(nil)

func (s *PgStore) LatestCorsPresetChangeID(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: CORS preset change log has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM cors_preset_change_log`).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest CORS preset change id: %w", err)
	}
	return id, nil
}

// LatestAccountCorsPresetChangeID returns the desired account-level revision.
// It intentionally covers account-wide and app-scoped presets owned by the
// account; gateway cursors are global within this ledger and can be compared
// directly with this per-account high-water mark.
func (s *PgStore) LatestAccountCorsPresetChangeID(ctx context.Context, accountID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: CORS preset policy status has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM cors_preset_change_log
		WHERE account_id = $1
	`, accountID).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest account CORS preset change id: %w", err)
	}
	return id, nil
}

func (s *PgStore) ListCorsPresetChangesAfter(ctx context.Context, afterID int64, limit int) ([]CorsPresetChange, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: CORS preset change log has nil pool")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id, preset_id, operation, created_at
		FROM cors_preset_change_log
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list CORS preset changes: %w", err)
	}
	defer rows.Close()
	changes := make([]CorsPresetChange, 0, limit)
	for rows.Next() {
		var change CorsPresetChange
		if err := rows.Scan(&change.ID, &change.AccountID, &change.PresetID, &change.Operation, &change.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan CORS preset change: %w", err)
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate CORS preset changes: %w", err)
	}
	return changes, nil
}

// PruneCorsPresetChangeLog keeps replay history until every active serving
// gateway has advanced past it, and retains the latest row for each live
// account so desired revisions do not regress after history pruning. Rows for
// deleted accounts can be removed after every gateway catches up. A serving
// gateway with no watermark holds pruning at zero.
func (s *PgStore) PruneCorsPresetChangeLog(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: CORS preset change log has nil pool")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("state: CORS preset change log prune requires cutoff")
	}
	n, err := sqlc.New().PruneFencedCorsPresetChangeLog(ctx, s.pool, sqlc.PruneFencedCorsPresetChangeLogParams{
		Before: pgtype.Timestamptz{Time: before.UTC(), Valid: true}, FreshnessSeconds: api.TrafficRuntimeObservationFreshness.Seconds(),
	})
	if err != nil {
		return 0, fmt.Errorf("state: prune CORS preset change log: %w", err)
	}
	return n, nil
}

// UpsertGatewayCorsPresetWatermark preserves the legacy writer API.
// These rows do not authorize convergence status or pruning.
func (s *PgStore) UpsertGatewayCorsPresetWatermark(ctx context.Context, nodeName, bootID string, lastChangeID int64) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: CORS preset policy status has nil pool")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" || bootID == "" || lastChangeID < 0 {
		return fmt.Errorf("state: invalid gateway CORS preset watermark")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO gateway_cors_preset_watermarks
		    (node_name, boot_id, last_change_id, observed_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (node_name) DO UPDATE SET
		    boot_id = EXCLUDED.boot_id,
		    last_change_id = CASE
		        WHEN gateway_cors_preset_watermarks.boot_id = EXCLUDED.boot_id
		        THEN GREATEST(gateway_cors_preset_watermarks.last_change_id, EXCLUDED.last_change_id)
		        ELSE EXCLUDED.last_change_id
		    END,
		    observed_at = now()
	`, nodeName, bootID, lastChangeID)
	if err != nil {
		return fmt.Errorf("state: upsert gateway CORS preset watermark: %w", err)
	}
	return nil
}
