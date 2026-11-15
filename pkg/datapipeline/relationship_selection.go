package datapipeline

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline/sqlc"
)

// Reject unsupported join keys before saving an owner preview. The worker still
// validates the owned target's exact schema before generating or granting SQL.
func (s *PostgresStore) checkedRelationships(ctx context.Context, tx pgx.Tx, p Pipeline, serving *api.DataPipelineServing) error {
	candidate := p
	candidate.Serving = serving
	if err := s.checkedTables(ctx, tx, candidate, p.Tables); err != nil {
		return err
	}
	r, err := s.q.PipelineCheckForCreate(ctx, tx, sqlc.PipelineCheckForCreateParams{AccountID: p.AccountID, ConnectionID: p.SourceConnectionID})
	if err != nil {
		return storeError(err)
	}
	check, err := describeCheck(sqlc.PipelineCheckGetRow(r))
	if err != nil {
		return err
	}
	relations := make([]relation, 0, len(p.Tables))
	for _, table := range check.Tables {
		if !slices.Contains(p.Tables, table.Name) {
			continue
		}
		projected, err := projectTable(table, serving)
		if err != nil {
			return err
		}
		rel := relation{Name: table.Name}
		for _, col := range projected.Columns {
			rel.Columns = append(rel.Columns, column{Name: col.Name, Type: col.Type, Key: col.PrimaryKey})
		}
		relations = append(relations, rel)
	}
	for _, rel := range serving.Relationships {
		if _, err = relationshipBody(candidate, rel, relations); err != nil {
			return err
		}
	}
	return nil
}
