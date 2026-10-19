package datapipeline

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func inspectionIssue(code, message, remediation string, passed bool) api.DataConnectionIssue {
	state := "blocked"
	if passed {
		state = "passed"
		remediation = ""
	}
	return api.DataConnectionIssue{Code: code, State: state, Message: message, Remediation: remediation}
}

func inspectSourceSettings(ctx context.Context, source *pgxpool.Pool) ([]api.DataConnectionIssue, error) {
	var version int
	var level string
	var retention int64
	var publish, replicate bool
	err := source.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer,current_setting('wal_level'),
 pg_size_bytes(current_setting('max_slot_wal_keep_size')),has_database_privilege(current_database(),'CREATE'),
 (SELECT rolreplication OR rolsuper FROM pg_roles WHERE rolname=current_user)`).Scan(&version, &level, &retention, &publish, &replicate)
	if err != nil {
		return nil, err
	}
	return []api.DataConnectionIssue{
		inspectionIssue("source_postgres_16_required", "PostgreSQL version", "Use a PostgreSQL 16 source.", version >= 160000 && version < 170000),
		inspectionIssue("source_logical_replication_required", "Logical replication", "Set wal_level=logical and restart the source if required by its provider.", level == "logical"),
		inspectionIssue("source_wal_retention_limit_required", "Finite replication retention", "Set max_slot_wal_keep_size to a positive value no greater than 1GB.", retention > 0 && retention <= api.DataPipelineRetainedWALMaxBytes),
		inspectionIssue("source_publication_permission_required", "Publication permission", "Grant the source role CREATE on this database.", publish),
		inspectionIssue("source_replication_permission_required", "Replication permission", "Grant the source role REPLICATION using your provider's supported procedure.", replicate),
	}, nil
}

// Catalog inspection never reads customer rows. This exact inspector also
// constructs the schema used by replication; discovery cannot invent support.
func inspectSourceTable(ctx context.Context, source *pgxpool.Pool, name string) (relation, api.DataSourceTable, error) {
	r := relation{Name: name}
	table := api.DataSourceTable{Name: name, Supported: true, Columns: []api.DataSourceColumn{}, Issues: []api.DataConnectionIssue{}}
	add := func(code, message, remediation, column string) {
		issue := inspectionIssue(code, message, remediation, false)
		issue.Column = column
		table.Issues = append(table.Issues, issue)
		table.Supported = false
	}
	if !identifier.MatchString(name) || strings.HasPrefix(name, "gregale_") {
		add("source_table_unsupported", "Table name is unsupported", "Use a lowercase SQL identifier beginning with a letter, outside the gregale_ namespace.", "")
	}
	var ordinary, readable, owner bool
	err := source.QueryRow(ctx, `SELECT c.oid,c.relkind='r' AND NOT c.relispartition AND NOT c.relrowsecurity AND c.relreplident='d',
 has_table_privilege(c.oid,'SELECT'),pg_has_role(c.relowner,'USAGE')
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=$1`, name).Scan(&r.OID, &ordinary, &readable, &owner)
	if errors.Is(err, pgx.ErrNoRows) {
		add("source_table_unsupported", "Table is unavailable", "Select an existing public table and run the check again.", "")
		return r, table, nil
	}
	if err != nil {
		return r, table, err
	}
	if !ordinary {
		add("source_table_unsupported", "Table structure is unsupported", "Use an ordinary, nonpartitioned table without source RLS and with default replica identity.", "")
	}
	if !readable {
		add("source_table_unsupported", "Table cannot be read", "Grant the source role SELECT on this table.", "")
	}
	if !owner {
		add("source_table_unsupported", "Publication cannot include this table", "Use a source role with ownership rights on this table.", "")
	}
	rows, err := source.Query(ctx, `SELECT a.attname,a.atttypmod,a.attnotnull,
 EXISTS(SELECT 1 FROM pg_index i WHERE i.indrelid=a.attrelid AND i.indisprimary AND a.attnum=ANY(i.indkey)),
 t.typname,nt.nspname,a.attgenerated::text,a.attnum,a.atthasdef,a.atthasmissing,a.attidentity<>''
 FROM pg_attribute a JOIN pg_class c ON c.oid=a.attrelid JOIN pg_namespace n ON n.oid=c.relnamespace
 JOIN pg_type t ON t.oid=a.atttypid JOIN pg_namespace nt ON nt.oid=t.typnamespace
 WHERE n.nspname='public' AND c.relname=$1 AND a.attnum>0 AND NOT a.attisdropped ORDER BY a.attnum LIMIT $2`, name, api.DataPipelineColumnsMax+1)
	if err != nil {
		return r, table, err
	}
	defer rows.Close()
	hasKey := false
	for rows.Next() {
		var col column
		var typ, namespace, generated string
		if err = rows.Scan(&col.Name, &col.Modifier, &col.NotNull, &col.Key, &typ, &namespace, &generated, &col.Ordinal, &col.Default, &col.Missing, &col.Identity); err != nil {
			return r, table, err
		}
		col.Type = typ
		supported := namespace == "pg_catalog" && generated == "" && supportedType(typ)
		if typ == "varchar" || typ == "bpchar" {
			base := "character varying"
			if typ == "bpchar" {
				base = "character"
			}
			if typ == "varchar" && col.Modifier == -1 {
				col.Type = base
			} else if col.Modifier < 5 || col.Modifier > 1048580 {
				supported = false
			} else {
				col.Type = base + "(" + strconv.FormatInt(int64(col.Modifier-4), 10) + ")"
			}
		}
		if !supported {
			col.Unsupported = true
			add("source_column_unsupported", "Column cannot be replicated", "Use a supported builtin type and a stored, nongenerated column.", col.Name)
		}
		table.Columns = append(table.Columns, api.DataSourceColumn{Name: col.Name, Type: col.Type, Nullable: !col.NotNull, PrimaryKey: col.Key})
		r.Columns = append(r.Columns, col)
		hasKey = hasKey || col.Key
	}
	if err = rows.Err(); err != nil {
		return r, table, err
	}
	if !hasKey {
		add("source_primary_key_required", "Primary key is missing", "Add a primary key before selecting this table.", "")
	}
	if len(r.Columns) > api.DataPipelineColumnsMax {
		add("source_column_unsupported", "Table exceeds the column limit", "Select a table with at most 128 columns.", "")
	}
	return r, table, nil
}
