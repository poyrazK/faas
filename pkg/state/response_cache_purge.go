package state

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
)

// ResponseCachePurgeChange is one durable app-scoped cache purge request.
// The global ID is also each gateway's broadcast replay cursor.
type ResponseCachePurgeChange struct {
	ID        int64
	AppID     string
	PathGlob  string
	Tag       string
	CreatedAt time.Time
}

// ResponseCachePurgeChangeLogStore exposes durable request creation, replay,
// gateway acknowledgements, and runtime status for the optional cache layer.
type ResponseCachePurgeChangeLogStore interface {
	CreateResponseCachePurge(context.Context, string, string, string) (int64, error)
	LatestResponseCachePurgeID(context.Context) (int64, error)
	LatestAppResponseCachePurgeID(context.Context, string) (int64, error)
	ListResponseCachePurgesAfter(context.Context, int64, int) ([]ResponseCachePurgeChange, error)
	BootstrapGatewayResponseCachePurgeCursor(context.Context, string) (int64, error)
	UpsertGatewayResponseCachePurgeWatermark(context.Context, string, int64) error
	PruneResponseCachePurgeChangeLog(context.Context, time.Time) (int64, error)
}

var _ ResponseCachePurgeChangeLogStore = (*PgStore)(nil)

func (s *PgStore) LatestResponseCachePurgeID(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: response cache purge log has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM response_cache_purge_change_log`).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest response cache purge id: %w", err)
	}
	return id, nil
}

// CreateResponseCachePurge commits the durable request and its fast-path
// notification atomically. The advisory lock is acquired before the identity
// value is allocated so MAX(id) remains a safe committed replay cursor.
func (s *PgStore) CreateResponseCachePurge(ctx context.Context, appID, pathGlob, tag string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: response cache purge has nil pool")
	}
	appID = strings.TrimSpace(appID)
	if appID == "" || (tag != "" && pathGlob != "") || len(pathGlob) > api.CachePurgeGlobMaxBytes {
		return 0, ErrInvalidArgument
	}
	if pathGlob != "" && pathGlob != "*" {
		if _, err := path.Match(pathGlob, "/"); err != nil {
			return 0, ErrInvalidArgument
		}
	}
	if tag != "" {
		canonical, err := api.NormalizeCacheTag(tag)
		if err != nil {
			return 0, ErrInvalidArgument
		}
		tag = canonical
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin response cache purge: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(711901248673)`); err != nil {
		return 0, fmt.Errorf("state: lock response cache purge sequence: %w", err)
	}
	var id int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO response_cache_purge_change_log (app_id, path_glob, tag)
		VALUES ($1, $2, $3)
		RETURNING id
	`, appID, pathGlob, tag).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: insert response cache purge: %w", mapErr(err))
	}
	payload, err := json.Marshal(struct {
		ID       int64  `json:"id"`
		AppID    string `json:"app_id"`
		PathGlob string `json:"path_glob"`
		Tag      string `json:"tag,omitempty"`
	}{ID: id, AppID: appID, PathGlob: pathGlob, Tag: tag})
	if err != nil {
		return 0, fmt.Errorf("state: encode response cache purge notification: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, db.NotifyCachePurge, string(payload)); err != nil {
		return 0, fmt.Errorf("state: notify response cache purge: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit response cache purge: %w", err)
	}
	return id, nil
}

func (s *PgStore) LatestAppResponseCachePurgeID(ctx context.Context, appID string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: response cache purge status has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(MAX(id), 0)
		FROM response_cache_purge_change_log
		WHERE app_id = $1
	`, appID).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest app response cache purge: %w", err)
	}
	return id, nil
}

func (s *PgStore) ListResponseCachePurgesAfter(ctx context.Context, afterID int64, limit int) ([]ResponseCachePurgeChange, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: response cache purge log has nil pool")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id::text, path_glob, tag, created_at
		FROM response_cache_purge_change_log
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list response cache purges: %w", err)
	}
	defer rows.Close()
	changes := make([]ResponseCachePurgeChange, 0, limit)
	for rows.Next() {
		var change ResponseCachePurgeChange
		if err := rows.Scan(&change.ID, &change.AppID, &change.PathGlob, &change.Tag, &change.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan response cache purge: %w", err)
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate response cache purges: %w", err)
	}
	return changes, nil
}

// BootstrapGatewayResponseCachePurgeCursor resumes a known gateway's last
// durable position. A new gateway starts from the highest position already
// applied by a serving peer: the shared Redis tier has seen every purge up to
// that point, while the new process's local cache starts empty. If no peer has
// reported yet, replay starts at zero.
func (s *PgStore) BootstrapGatewayResponseCachePurgeCursor(ctx context.Context, nodeName string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: response cache purge watermark has nil pool")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" {
		return 0, ErrInvalidArgument
	}
	var cursor int64
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(
		    (SELECT last_change_id
		     FROM gateway_response_cache_purge_watermarks
	     WHERE node_name = $1),
		    (SELECT MAX(w.last_change_id)
		     FROM compute_nodes n
		     JOIN gateway_response_cache_purge_watermarks w ON w.node_name = n.name
		     WHERE n.name <> $1
		       AND n.active = true
		       AND n.role IN ('compute-only', 'compute-node')
		       AND n.gateway_target_url IS NOT NULL
		       AND btrim(n.gateway_target_url) <> ''),
		    0
		)
	`, nodeName).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("state: bootstrap response cache purge cursor: %w", err)
	}
	return cursor, nil
}

func (s *PgStore) UpsertGatewayResponseCachePurgeWatermark(ctx context.Context, nodeName string, lastChangeID int64) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: response cache purge watermark has nil pool")
	}
	nodeName = strings.TrimSpace(nodeName)
	if nodeName == "" || lastChangeID < 0 {
		return ErrInvalidArgument
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO gateway_response_cache_purge_watermarks
		    (node_name, last_change_id, observed_at)
		VALUES ($1, $2, now())
		ON CONFLICT (node_name) DO UPDATE SET
		    last_change_id = GREATEST(gateway_response_cache_purge_watermarks.last_change_id, EXCLUDED.last_change_id),
		    observed_at = now()
	`, nodeName, lastChangeID)
	if err != nil {
		return fmt.Errorf("state: upsert response cache purge watermark: %w", err)
	}
	return nil
}

func (s *PgStore) PruneResponseCachePurgeChangeLog(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: response cache purge log has nil pool")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("state: response cache purge log prune requires cutoff")
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM response_cache_purge_change_log
		WHERE created_at < $1
		  AND id <= COALESCE((
		      SELECT MIN(COALESCE(w.last_change_id, 0))
		      FROM compute_nodes n
		      LEFT JOIN gateway_response_cache_purge_watermarks w ON w.node_name = n.name
		      WHERE n.active = true
	        AND n.role IN ('compute-only', 'compute-node')
	        AND n.gateway_target_url IS NOT NULL
	        AND btrim(n.gateway_target_url) <> ''
		  ), 9223372036854775807::bigint)
		  AND id < (
		      SELECT MAX(current.id)
		      FROM response_cache_purge_change_log current
		      WHERE current.app_id = response_cache_purge_change_log.app_id
		  )
	`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune response cache purge log: %w", err)
	}
	return tag.RowsAffected(), nil
}
