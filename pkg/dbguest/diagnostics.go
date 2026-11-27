package dbguest

import "time"

const (
	DiagnosticsAvailable   = "available"
	DiagnosticsParked      = "parked"
	DiagnosticsUnavailable = "unavailable"
)

type Diagnostics struct {
	State  string          `json:"state"`
	Sample *ActivitySample `json:"sample,omitempty"`
}

// ActivitySample covers one primary. Connection usage is instance-wide,
// while transactions and blocked sessions are scoped to the managed database.
type ActivitySample struct {
	SampledAt                 time.Time  `json:"sampled_at"`
	Connections               int64      `json:"connections"`
	ConnectionLimit           int64      `json:"connection_limit"`
	ActiveTransactions        int64      `json:"active_transactions"`
	OldestActiveTransactionMS int64      `json:"oldest_active_transaction_ms"`
	IdleInTransaction         int64      `json:"idle_in_transaction"`
	OldestIdleTransactionMS   int64      `json:"oldest_idle_transaction_ms"`
	LockWaits                 int64      `json:"lock_waits"`
	BlockedSessions           []LockWait `json:"blocked_sessions"`
}

type LockWait struct {
	PID          int32   `json:"pid"`
	BlockingPIDs []int32 `json:"blocking_pids"`
}

// ActivitySQL is a bounded read of the primary's current statistics. The
// observer is excluded from connection usage and the transaction lists.
const ActivitySQL = `BEGIN READ ONLY;
SET LOCAL statement_timeout = '2s';
SET LOCAL search_path = pg_catalog;
WITH sessions AS MATERIALIZED (
    SELECT pid, datname, state, wait_event_type, xact_start
    FROM pg_catalog.pg_stat_activity
    WHERE backend_type = 'client backend' AND pid <> pg_catalog.pg_backend_pid()
), target AS MATERIALIZED (
    SELECT pid, state, wait_event_type, xact_start FROM sessions WHERE datname = current_database()
), blocked AS MATERIALIZED (
    SELECT pid FROM target WHERE wait_event_type = 'Lock'
    ORDER BY xact_start NULLS LAST, pid
    LIMIT 20
)
SELECT json_build_object(
    'sampled_at', statement_timestamp(),
    'connections', (SELECT count(*) FROM sessions),
    'connection_limit', current_setting('max_connections')::integer -
        current_setting('superuser_reserved_connections')::integer -
        coalesce(current_setting('reserved_connections', true)::integer, 0),
    'active_transactions', count(*) FILTER (WHERE state = 'active' AND xact_start IS NOT NULL),
    'oldest_active_transaction_ms', greatest(0, coalesce(
        max(extract(epoch FROM statement_timestamp() - xact_start) * 1000) FILTER (WHERE state = 'active'), 0
    ))::bigint,
    'idle_in_transaction', count(*) FILTER (WHERE state IN ('idle in transaction', 'idle in transaction (aborted)')),
    'oldest_idle_transaction_ms', greatest(0, coalesce(
        max(extract(epoch FROM statement_timestamp() - xact_start) * 1000)
            FILTER (WHERE state IN ('idle in transaction', 'idle in transaction (aborted)')), 0
    ))::bigint,
    'lock_waits', count(*) FILTER (WHERE wait_event_type = 'Lock'),
    'blocked_sessions', (SELECT coalesce(json_agg(json_build_object(
        'pid', pid, 'blocking_pids', ARRAY(
            SELECT DISTINCT blocker FROM unnest(pg_catalog.pg_blocking_pids(pid)) AS blocker
            ORDER BY blocker LIMIT 20
        )
    ) ORDER BY pid), '[]'::json) FROM blocked)
) FROM target;
COMMIT;`
