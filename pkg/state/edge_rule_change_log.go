package state

import (
	"context"
	"fmt"
	"time"
)

// EdgeRuleChange is one durable edge-rule mutation. The gateway repair loop
// currently needs only the monotonic ID, but retaining the mutation shape in
// the store seam keeps the ledger useful for diagnostics and future replay.
type EdgeRuleChange struct {
	ID         int64
	AppID      string
	RuleID     string
	Operation  string
	MatchHosts []string
	CreatedAt  time.Time
}

// EdgeRuleChangeLogStore is an additive capability used by gatewayd. It is
// deliberately separate from Store so small test stores do not need to grow
// for a production-only repair loop.
type EdgeRuleChangeLogStore interface {
	LatestEdgeRuleChangeID(context.Context) (int64, error)
	ListEdgeRuleChangesAfter(context.Context, int64, int) ([]EdgeRuleChange, error)
	PruneEdgeRuleChangeLog(context.Context, time.Time) (int64, error)
}

var _ EdgeRuleChangeLogStore = (*PgStore)(nil)

// LatestEdgeRuleChangeID returns the durable high-water mark. A zero result
// is valid when the ledger has not observed a mutation yet.
func (s *PgStore) LatestEdgeRuleChangeID(ctx context.Context) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: edge-rule change log has nil pool")
	}
	var id int64
	if err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(id), 0) FROM edge_rule_change_log`).Scan(&id); err != nil {
		return 0, fmt.Errorf("state: latest edge-rule change id: %w", err)
	}
	return id, nil
}

// ListEdgeRuleChangesAfter reads the ledger in ID order. The limit is bounded
// at the store seam so a future caller cannot turn a repair pass into an
// unbounded allocation from database-controlled history.
func (s *PgStore) ListEdgeRuleChangesAfter(ctx context.Context, afterID int64, limit int) ([]EdgeRuleChange, error) {
	if s == nil || s.pool == nil {
		return nil, fmt.Errorf("state: edge-rule change log has nil pool")
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, app_id, rule_id, operation, match_hosts, created_at
		FROM edge_rule_change_log
		WHERE id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, fmt.Errorf("state: list edge-rule changes: %w", err)
	}
	defer rows.Close()
	changes := make([]EdgeRuleChange, 0, limit)
	for rows.Next() {
		var change EdgeRuleChange
		if err := rows.Scan(&change.ID, &change.AppID, &change.RuleID, &change.Operation, &change.MatchHosts, &change.CreatedAt); err != nil {
			return nil, fmt.Errorf("state: scan edge-rule change: %w", err)
		}
		changes = append(changes, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: iterate edge-rule changes: %w", err)
	}
	return changes, nil
}

// PruneEdgeRuleChangeLog bounds the retained ledger. The repair loop keeps
// a recent window for gateways that are temporarily offline; a restarted
// gateway baselines to the current high-water mark before polling.
func (s *PgStore) PruneEdgeRuleChangeLog(ctx context.Context, before time.Time) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("state: edge-rule change log has nil pool")
	}
	if before.IsZero() {
		return 0, fmt.Errorf("state: edge-rule change log prune requires cutoff")
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM edge_rule_change_log WHERE created_at < $1`, before.UTC())
	if err != nil {
		return 0, fmt.Errorf("state: prune edge-rule change log: %w", err)
	}
	return tag.RowsAffected(), nil
}
