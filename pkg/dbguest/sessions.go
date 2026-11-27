package dbguest

import "time"

const MaxSessionRows = 50

type Sessions struct {
	State  string         `json:"state"`
	Sample *SessionSample `json:"sample,omitempty"`
}

type SessionSample struct {
	SampledAt time.Time    `json:"sampled_at"`
	HasMore   bool         `json:"has_more"`
	Sessions  []SessionRow `json:"sessions"`
}

type SessionRow struct {
	PID              int32  `json:"pid"`
	State            string `json:"state"`
	TransactionAgeMS int64  `json:"transaction_age_ms"`
	WaitEventType    string `json:"wait_event_type,omitempty"`
	WaitEvent        string `json:"wait_event,omitempty"`
	ApplicationName  string `json:"application_name,omitempty"`
	// TargetID is assigned by the scheduler from the proxy's live snapshot.
	TargetID string `json:"target_id,omitempty"`
}

// SessionSQL returns only client metadata from the selected primary.
// Limit is one more than MaxSessionRows so the caller can report truncation.
const SessionSQL = `BEGIN READ ONLY;
SET LOCAL statement_timeout = '2s';
SET LOCAL search_path = pg_catalog;
SELECT json_build_object(
    'sampled_at', statement_timestamp(),
    'sessions', coalesce(json_agg(row_to_json(s) ORDER BY priority, transaction_age_ms DESC, pid), '[]'::json)
)
FROM (
    SELECT pid, coalesce(state, 'unknown') AS state,
        CASE WHEN state='active' THEN 0 WHEN state LIKE 'idle in transaction%' THEN 1 ELSE 2 END AS priority,
        greatest(0, coalesce(extract(epoch FROM statement_timestamp()-xact_start)*1000, 0))::bigint AS transaction_age_ms,
        coalesce(wait_event_type, '') AS wait_event_type, coalesce(wait_event, '') AS wait_event,
        left(coalesce(application_name, ''), 64) AS application_name
    FROM pg_stat_activity
    WHERE backend_type='client backend' AND datname=current_database() AND pid<>pg_backend_pid()
    ORDER BY priority, transaction_age_ms DESC, pid LIMIT 51
) s;
COMMIT;`
