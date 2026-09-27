package state

import (
	"context"
	"fmt"
	"time"
)

// DeleteOutboundFlowIPLeasesBefore removes a bounded batch of closed leases.
// Open leases are retained until a lifecycle transition closes them.
func (s *PgStore) DeleteOutboundFlowIPLeasesBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, fmt.Errorf("outbound flow IP leases: nil database pool")
	}
	if limit <= 0 || limit > 10000 {
		return 0, fmt.Errorf("outbound flow IP leases: invalid retention batch size %d", limit)
	}
	tag, err := s.pool.Exec(ctx, `
WITH expired AS (
    SELECT id FROM outbound_flow_ip_leases
     WHERE active_until < $1
     ORDER BY active_until, id
     LIMIT $2
)
DELETE FROM outbound_flow_ip_leases l USING expired
 WHERE l.id = expired.id`, cutoff.UTC(), limit)
	if err != nil {
		return 0, fmt.Errorf("outbound flow IP leases: delete expired: %w", err)
	}
	return tag.RowsAffected(), nil
}
