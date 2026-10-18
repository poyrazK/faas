package state

import (
	"context"
	"time"
)

// AIGatewayLogExport is one AI gateway line for an app's log drains
// (ADR-1340). ID orders the lines and is the drains' source sequence.
type AIGatewayLogExport struct {
	ID         int64
	AccountID  string
	CreatedAt  time.Time
	Line       string
	Attributes map[string]string
}

// ListAIGatewayLogExports returns an app's export lines after afterID, in
// ID order, that are at least visibility old: IDs are taken before commit,
// so a line younger than a settlement can live might still be joined by a
// lower ID. createdAfter bounds a fresh cursor (afterID zero).
func (s *PgStore) ListAIGatewayLogExports(ctx context.Context, appID string, afterID int64, createdAfter time.Time,
	visibility time.Duration, limit int) ([]AIGatewayLogExport, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, account_id::text, created_at, line, attributes
		  FROM outbound_ai_log_exports
		 WHERE app_id = $1 AND id > $2 AND created_at >= $3
		   AND created_at <= now() - make_interval(secs => $4::float8)
		 ORDER BY id
		 LIMIT $5`, mustPgUUID(appID), afterID, createdAfter, visibility.Seconds(), limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	var out []AIGatewayLogExport
	for rows.Next() {
		var export AIGatewayLogExport
		if err := rows.Scan(&export.ID, &export.AccountID, &export.CreatedAt, &export.Line, &export.Attributes); err != nil {
			return nil, mapErr(err)
		}
		export.CreatedAt = export.CreatedAt.UTC()
		out = append(out, export)
	}
	return out, mapErr(rows.Err())
}
