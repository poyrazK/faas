package dbqueryinsights

import "github.com/onebox-faas/faas/pkg/dbmigration"

// InstallSQL runs in the private maintenance database.
const InstallSQL = `BEGIN;
SET LOCAL statement_timeout = '2s';
SET LOCAL lock_timeout = '500ms';
SET LOCAL search_path = pg_catalog;
CREATE SCHEMA IF NOT EXISTS gregale_query_insights;
REVOKE ALL ON SCHEMA gregale_query_insights FROM PUBLIC;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements WITH SCHEMA gregale_query_insights;
COMMIT;`

func SampleSQL(database string) string {
	return `BEGIN READ ONLY;
SET LOCAL statement_timeout = '2s';
SET LOCAL search_path = pg_catalog;
SELECT json_build_object(
    'at', statement_timestamp(),
    'boot_at', pg_postmaster_start_time(),
    'reset_at', info.stats_reset,
    'evictions', info.dealloc,
    'counters', (
        SELECT coalesce(json_agg(json_build_object(
            'query_id', queryid::text, 'user_id', userid::bigint, 'calls', calls,
            'execution_ms', total_exec_time
        )), '[]'::json)
        FROM gregale_query_insights.pg_stat_statements(false)
        WHERE dbid = (SELECT oid FROM pg_database WHERE datname = ` + dbmigration.Literal(database) + `)
          AND toplevel AND queryid <> 0
    )
) FROM gregale_query_insights.pg_stat_statements_info info;
COMMIT;`
}

// RankedSQL aggregates only completed intervals inside the requested window.
const RankedSQL = `
SELECT i.boot_at, c.query_id, sum(c.calls)::bigint, sum(c.execution_ms)
FROM native_postgres_query_intervals i
CROSS JOIN LATERAL jsonb_to_recordset(i.counters) AS c(query_id text,calls bigint,execution_ms double precision)
WHERE i.database_id=$1::uuid AND i.started_at >= $2 AND i.ended_at <= $3
GROUP BY i.boot_at, c.query_id
ORDER BY sum(c.execution_ms) DESC, sum(c.calls) DESC, i.boot_at DESC, c.query_id
LIMIT 20
`
