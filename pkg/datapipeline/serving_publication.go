package datapipeline

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

func publicationTable(p Pipeline, table api.DataSourceTable) string {
	name := "ONLY " + tableIdent(table.Name)
	if len(selectedColumns(p.Serving, table.Name)) == 0 {
		return name
	}
	columns := make([]string, 0, len(table.Columns))
	for _, c := range table.Columns {
		columns = append(columns, ident(c.Name))
	}
	return name + " (" + strings.Join(columns, ",") + ")"
}

func checkProjectedPublication(ctx context.Context, p Pipeline, source *pgxpool.Pool) error {
	rows, err := source.Query(ctx, `SELECT schemaname,tablename,attnames,coalesce(rowfilter,'') FROM pg_publication_tables WHERE pubname=$1 ORDER BY tablename COLLATE "C"`, pipelineName(p))
	if err != nil {
		return err
	}
	type published struct {
		schema, table, filter string
		columns               []string
	}
	var actual []published
	for rows.Next() {
		var v published
		if err = rows.Scan(&v.schema, &v.table, &v.columns, &v.filter); err != nil {
			break
		}
		actual = append(actual, v)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if len(actual) != len(p.Tables) {
		return &blocked{"source_publication_changed", true}
	}
	for _, v := range actual {
		if v.schema != "public" || !slices.Contains(p.Tables, v.table) || v.filter != "" {
			return &blocked{"source_publication_changed", true}
		}
		_, table, err := inspectPipelineTable(ctx, p, source, v.table)
		if err != nil {
			return err
		}
		var expected []string
		for _, c := range table.Columns {
			expected = append(expected, c.Name)
		}
		matches := slices.Equal(expected, v.columns)
		if !matches && p.Evolution != nil && p.Evolution.State == "approved" && p.Evolution.Kind == "projection" && len(p.Evolution.AddedTables) == 0 && p.Evolution.Before != nil {
			previous := p
			previous.Serving = p.Evolution.Before
			_, before, beforeErr := inspectPipelineTable(ctx, previous, source, v.table)
			var columns []string
			for _, c := range before.Columns {
				columns = append(columns, c.Name)
			}
			matches = beforeErr == nil && before.Supported && slices.Equal(columns, v.columns)
		}
		if !table.Supported || !matches {
			return &blocked{"source_publication_changed", true}
		}
	}
	return nil
}

// The target DDL receipt is already committed and native apply is suspended.
// A lost source commit is retried before enabling apply or verification.
func advanceProjectedPublication(ctx context.Context, p Pipeline, source *pgxpool.Pool, current func(context.Context) error) error {
	if p.Evolution == nil || p.Evolution.State != "approved" || p.Evolution.Kind != "projection" || len(p.Evolution.AddedTables) > 0 {
		return nil
	}
	if p.SchemaChange == nil || p.SchemaApproval != p.SchemaChange.ID {
		return ErrConflict
	}
	tx, err := lockSchemaSource(ctx, p, source)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	actual := make([]relation, 0, len(p.Tables))
	tables := make([]string, 0, len(p.Tables))
	for _, name := range p.Tables {
		r, table, err := inspectPipelineTable(ctx, p, tx, name)
		if err != nil {
			return err
		}
		actual = append(actual, r)
		tables = append(tables, publicationTable(p, table))
	}
	if schemaPlan(p, p.SchemaChange.Before, actual).ID != p.SchemaApproval {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "ALTER PUBLICATION "+ident(pipelineName(p))+" SET TABLE "+strings.Join(tables, ",")); err != nil {
		return err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_publication WHERE pubname=$1 AND obj_description(oid,'pg_publication')=$2
 AND NOT puballtables AND pubinsert AND pubupdate AND pubdelete AND pubtruncate AND NOT pubviaroot)`, pipelineName(p), marker(p)).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return ErrConflict
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
