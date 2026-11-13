package datapipeline

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Hash the installed functions and complete permission inventory. A new binding
// or replaced function invalidates the acknowledgement even after owner approval
// was applied. Replays retain the version and each runtime restart's wake ID.
func (e *Engine) refreshRelationships(ctx context.Context, p Pipeline, target *pgx.Conn, ob *Observation, current func(context.Context) error) (bool, error) {
	if len(p.Serving.Relationships) == 0 && (p.Evolution == nil || p.Evolution.Kind != "relationships") {
		return true, nil
	}
	var inventory, saved string
	if err := target.QueryRow(ctx, `SELECT coalesce(jsonb_agg(jsonb_build_array(r.name,r.body_hash,r.function_oid,f.proacl::text) ORDER BY r.name),'[]'::jsonb)::text
 FROM gregale_pipeline.relationships r JOIN pg_proc f ON f.oid=r.function_oid WHERE r.pipeline_id=$1 AND r.generation=$2`, p.ID, p.Generation).Scan(&inventory); err != nil {
		return false, err
	}
	version := fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:%d:%s", p.ID, p.Generation, inventory))))
	if err := target.QueryRow(ctx, `SELECT relationship_refresh FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation).Scan(&saved); err != nil {
		return false, err
	}
	if saved == version {
		return true, nil
	}
	refresh := e.RefreshRelationships
	if refresh == nil {
		refresh = e.RefreshSchema
	}
	if refresh == nil {
		ob.State, ob.Reason = "blocked", "relationship_api_refresh_unavailable"
		return false, nil
	}
	attempt := 0
	if p.Evolution != nil {
		attempt = p.Evolution.Retry
	}
	_, ready, err := refresh(ctx, p, SchemaChange{ID: version, Retry: attempt})
	if err != nil {
		ob.State, ob.Reason = "blocked", "relationship_api_refresh_failed"
		return false, nil //nolint:nilerr // Failure is recorded in the blocked observation and requires explicit retry.
	}
	if !ready {
		ob.State, ob.Reason = "blocked", "relationship_api_refresh_pending"
		return false, nil
	}
	if err = current(ctx); err != nil {
		return false, err
	}
	_, err = target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET relationship_refresh=$3 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation, version)
	return err == nil, err
}
