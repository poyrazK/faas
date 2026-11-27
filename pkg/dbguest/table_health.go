package dbguest

import (
	"errors"
	"fmt"
	"time"
)

const (
	TableHealthDefaultLimit = 20
	// The page must fit MaxResponseBytes even with escaped identifiers.
	TableHealthMaxLimit = 50
)

type TableHealthOptions struct {
	Sort  string `json:"sort"`
	Limit int    `json:"limit"`
}

func (o TableHealthOptions) Validate() error {
	switch o.Sort {
	case "size", "dead-rows", "xid-age":
	default:
		return errors.New("sort must be size, dead-rows, or xid-age")
	}
	if o.Limit < 1 || o.Limit > TableHealthMaxLimit {
		return fmt.Errorf("limit must be between 1 and %d", TableHealthMaxLimit)
	}
	return nil
}

type TableHealth struct {
	State  string             `json:"state"`
	Sample *TableHealthSample `json:"sample,omitempty"`
}

type TableHealthSample struct {
	SampledAt time.Time        `json:"sampled_at"`
	HasMore   bool             `json:"has_more"`
	Tables    []TableHealthRow `json:"tables"`
}

type TableHealthRow struct {
	Schema              string     `json:"schema"`
	Name                string     `json:"name"`
	TableBytes          int64      `json:"table_bytes"`
	IndexBytes          int64      `json:"index_bytes"`
	EstimatedDeadRows   int64      `json:"estimated_dead_rows"`
	ChangesSinceAnalyze int64      `json:"changes_since_analyze"`
	XIDAge              int64      `json:"xid_age"`
	LastVacuumAt        *time.Time `json:"last_vacuum_at,omitempty"`
	LastAnalyzeAt       *time.Time `json:"last_analyze_at,omitempty"`
}

// TableHealthQuery ranks stored user tables and leaf partitions. For dead-row
// and XID sorts, only the returned tables incur relation size reads.
func TableHealthQuery(options TableHealthOptions) (string, error) {
	if err := options.Validate(); err != nil {
		return "", err
	}
	rank := "pg_total_relation_size(s.relid)"
	switch options.Sort {
	case "dead-rows":
		rank = "s.n_dead_tup"
	case "xid-age":
		rank = "greatest(age(c.relfrozenxid), age(toast.relfrozenxid))"
	}
	return fmt.Sprintf(`
BEGIN READ ONLY;
SET LOCAL search_path = pg_catalog;
SET LOCAL lock_timeout = '150ms';
SET LOCAL statement_timeout = '2s';
WITH ranked AS MATERIALIZED (
	SELECT s.relid, s.schemaname AS schema, s.relname AS name,
		s.n_dead_tup AS estimated_dead_rows, s.n_mod_since_analyze AS changes_since_analyze,
		greatest(age(c.relfrozenxid), age(toast.relfrozenxid)) AS xid_age,
		greatest(s.last_vacuum, s.last_autovacuum) AS last_vacuum_at,
		greatest(s.last_analyze, s.last_autoanalyze) AS last_analyze_at,
		%s AS sort_key
	FROM pg_stat_user_tables AS s
	JOIN pg_class AS c ON c.oid = s.relid
	LEFT JOIN pg_class AS toast ON toast.oid = c.reltoastrelid
	WHERE c.relkind = 'r' AND c.relpersistence <> 't'
	ORDER BY sort_key DESC NULLS LAST, schema, name, relid
	LIMIT %d
), page AS MATERIALIZED (
	SELECT schema, name, estimated_dead_rows, changes_since_analyze, xid_age,
		last_vacuum_at, last_analyze_at, pg_table_size(relid) AS table_bytes,
		pg_indexes_size(relid) AS index_bytes,
		row_number() OVER (ORDER BY sort_key DESC NULLS LAST, schema, name, relid) AS position
	FROM (
		SELECT * FROM ranked ORDER BY sort_key DESC NULLS LAST, schema, name, relid LIMIT %d
	) AS selected
)
SELECT jsonb_build_object(
	'sampled_at', clock_timestamp(),
	'has_more', (SELECT count(*) FROM ranked) > %d,
	'tables', coalesce(
		(SELECT jsonb_agg(to_jsonb(page) - 'position' ORDER BY position) FROM page),
		'[]'::jsonb
	)
);
COMMIT;`, rank, options.Limit+1, options.Limit, options.Limit), nil
}
