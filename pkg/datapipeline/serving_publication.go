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
		if !table.Supported || !slices.Equal(expected, v.columns) {
			return &blocked{"source_publication_changed", true}
		}
	}
	return nil
}
