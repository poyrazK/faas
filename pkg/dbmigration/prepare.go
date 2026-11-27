package dbmigration

import (
	"errors"
	"slices"
	"strings"
)

const MaxPreparationIndexes = 128

const Prepared = "prepared"

type Preparation struct {
	State        string               `json:"state"`
	Reason       string               `json:"reason,omitempty"`
	IndexesTotal int                  `json:"indexes_total"`
	IndexesReady int                  `json:"indexes_ready"`
	Sequences    int64                `json:"sequences"`
	Blockers     []PreparationBlocker `json:"blockers,omitempty"`
}

type PreparationBlocker struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// Index contains only catalog identifiers and primitive values. Source SQL
// expressions and deparsed DDL never reach the target.
type Index struct {
	Name      string   `json:"name"`
	Table     string   `json:"table"`
	Columns   []string `json:"columns"`
	Supported bool     `json:"supported"`
	Valid     bool     `json:"valid"`
	Bytes     int64    `json:"bytes"`
}

func (i Index) SameDefinition(other Index) bool {
	return i.Name == other.Name && i.Table == other.Table && i.Supported && other.Supported &&
		slices.Equal(i.Columns, other.Columns)
}

func (i Index) BuildSQL() (string, error) {
	if !i.Supported || !safePreparationName(i.Name) || !safePreparationName(i.Table) ||
		len(i.Columns) == 0 || len(i.Columns) > 32 {
		return "", errors.New("unsupported migration index")
	}
	columns := make([]string, 0, len(i.Columns))
	for _, name := range i.Columns {
		if !safeTable.MatchString("public." + name) {
			return "", errors.New("unsupported migration index column")
		}
		columns = append(columns, Ident(name))
	}
	return "CREATE INDEX CONCURRENTLY " + Ident(i.Name) + " ON public." + Ident(i.Table) +
		" USING btree (" + strings.Join(columns, ", ") + ");", nil
}

func safePreparationName(name string) bool {
	return safeTable.MatchString("public."+name) && !strings.HasPrefix(name, "gregale_")
}

// Keep the shape check independent of indisvalid so an interrupted build
// can be matched to its durable intent before it is removed.
const preparationIndexes = `
WITH indexes AS (
 SELECT idx.relname::text AS name, tab.relname::text AS "table",
   (SELECT json_agg(a.attname ORDER BY k.position)
    FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, position)
    JOIN pg_catalog.pg_attribute a ON a.attrelid=tab.oid AND a.attnum=k.attnum) AS columns,
   (tab.relkind='r' AND NOT tab.relispartition AND idx.relkind='i' AND am.amname='btree'
    AND NOT i.indisunique AND NOT i.indisexclusion AND NOT i.indisreplident AND NOT i.indisclustered
    AND i.indexprs IS NULL AND i.indpred IS NULL AND i.indnatts=i.indnkeyatts
    AND idx.reltablespace=0 AND idx.reloptions IS NULL
    AND NOT EXISTS (
      SELECT FROM unnest(i.indkey) WITH ORDINALITY AS k(attnum, position)
      LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid=tab.oid AND a.attnum=k.attnum
      LEFT JOIN pg_catalog.pg_opclass oc ON oc.oid=i.indclass[(k.position-1)::integer]
      WHERE a.attnum IS NULL OR oc.opcdefault IS DISTINCT FROM true
        OR oc.opcnamespace IS DISTINCT FROM 'pg_catalog'::regnamespace
        OR i.indoption[(k.position-1)::integer] IS DISTINCT FROM 0::smallint
        OR a.attcollation IS DISTINCT FROM i.indcollation[(k.position-1)::integer]
    )
    AND NOT EXISTS (SELECT FROM pg_catalog.pg_attribute a WHERE a.attrelid=idx.oid AND a.attoptions IS NOT NULL)
   ) AS supported,
   (i.indisvalid AND i.indisready AND i.indislive) AS valid,
   pg_catalog.pg_relation_size(idx.oid) AS bytes
 FROM pg_catalog.pg_index i
 JOIN pg_catalog.pg_class idx ON idx.oid=i.indexrelid
 JOIN pg_catalog.pg_class tab ON tab.oid=i.indrelid
 JOIN pg_catalog.pg_namespace n ON n.oid=tab.relnamespace
 JOIN pg_catalog.pg_am am ON am.oid=idx.relam
 WHERE n.nspname='public' AND NOT i.indisprimary
 ORDER BY idx.relname COLLATE "C" LIMIT 129
)`

// PreparationIndexesSQL is shared by the source and target. Matching every
// column, ordering and operator class shape prevents a name-only adoption.
const PreparationIndexesSQL = preparationIndexes + `
SELECT coalesce(json_agg(row_to_json(indexes) ORDER BY name COLLATE "C"),'[]'::json) FROM indexes`

const InspectPreparationSQL = preparationIndexes + `,
user_namespaces AS (
 SELECT oid,nspname FROM pg_catalog.pg_namespace WHERE nspname !~ '^pg_' AND nspname <> 'information_schema'
),
user_relations AS (
 SELECT c.* FROM pg_catalog.pg_class c JOIN user_namespaces n ON n.oid=c.relnamespace
)
SELECT json_build_object(
 'indexes',(SELECT coalesce(json_agg(row_to_json(indexes) ORDER BY name COLLATE "C"),'[]'::json) FROM indexes),
 'sequences',(SELECT count(*) FROM user_relations WHERE relkind='S'),
 'defaults',(SELECT count(*) FROM user_relations c JOIN pg_catalog.pg_attribute a ON a.attrelid=c.oid
   WHERE c.relkind='r' AND a.attnum>0 AND NOT a.attisdropped AND (a.atthasdef OR a.attidentity <> '')),
 'constraints',(SELECT count(*) FROM pg_catalog.pg_constraint c JOIN user_namespaces n ON n.oid=c.connamespace WHERE c.contype <> 'p'),
 'triggers',(SELECT count(*) FROM pg_catalog.pg_trigger t JOIN user_relations c ON c.oid=t.tgrelid WHERE NOT t.tgisinternal),
 'policies',(SELECT count(*) FROM pg_catalog.pg_policy p JOIN user_relations c ON c.oid=p.polrelid),
 'rules',(SELECT count(*) FROM pg_catalog.pg_rewrite r JOIN user_relations c ON c.oid=r.ev_class WHERE c.relkind='r'),
 'views',(SELECT count(*) FROM user_relations WHERE relkind IN ('v','m','f')),
 'routines',(SELECT count(*) FROM pg_catalog.pg_proc p JOIN user_namespaces n ON n.oid=p.pronamespace),
 'types',(SELECT count(*) FROM pg_catalog.pg_type t JOIN user_namespaces n ON n.oid=t.typnamespace
   WHERE t.typrelid=0 AND NOT (t.typelem<>0 AND t.typcategory='A')),
 'extensions',(SELECT count(*) FROM pg_catalog.pg_extension WHERE extname <> 'plpgsql'),
 'schemas',(SELECT count(*) FROM user_namespaces WHERE nspname <> 'public'),
 'table_grants',(SELECT count(*) FROM user_relations WHERE relkind IN ('r','v','m','S') AND relacl IS NOT NULL)
)`

type PreparationInventory struct {
	Indexes     []Index
	Sequences   int64
	Defaults    int64
	Constraints int64
	Triggers    int64
	Policies    int64
	Rules       int64
	Views       int64
	Routines    int64
	Types       int64
	Extensions  int64
	Schemas     int64
	TableGrants int64 `json:"table_grants"`
}

// Plan reports all known blockers while allowing safe index builds to make
// progress. Count limits apply before any work is scheduled.
func (inv PreparationInventory) Plan() (Preparation, []Index) {
	p := Preparation{State: Preparing, Sequences: max(inv.Sequences, 0)}
	if len(inv.Indexes) > MaxPreparationIndexes {
		p.Blockers = append(p.Blockers, PreparationBlocker{Code: "source_index_limit_exceeded", Count: int64(len(inv.Indexes))})
		return p, nil
	}
	var plan []Index
	var unsupported int64
	for _, index := range inv.Indexes {
		if _, err := index.BuildSQL(); err != nil || !index.Valid || index.Bytes < 0 {
			unsupported++
			continue
		}
		plan = append(plan, index)
	}
	p.IndexesTotal = len(plan)
	for _, issue := range []PreparationBlocker{
		{Code: "source_secondary_indexes_unsupported", Count: unsupported},
		{Code: "source_column_defaults_or_identity", Count: inv.Defaults},
		{Code: "source_constraints", Count: inv.Constraints},
		{Code: "source_triggers", Count: inv.Triggers},
		{Code: "source_policies", Count: inv.Policies},
		{Code: "source_rules", Count: inv.Rules},
		{Code: "source_views_or_foreign_tables", Count: inv.Views},
		{Code: "source_routines", Count: inv.Routines},
		{Code: "source_types", Count: inv.Types},
		{Code: "source_extensions", Count: inv.Extensions},
		{Code: "source_schemas", Count: inv.Schemas},
		{Code: "source_object_grants", Count: inv.TableGrants},
	} {
		if issue.Count > 0 {
			p.Blockers = append(p.Blockers, issue)
		}
	}
	return p, plan
}
