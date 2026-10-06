package state

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func readCloneSchemaCoverageDB(ctx context.Context, db sqlc.DBTX) (ProjectEnvironmentCloneSchemaCoverage, error) {
	rows, err := new(sqlc.Queries).ReadProjectEnvironmentCloneCoverageSchema(ctx, db)
	if err != nil {
		return ProjectEnvironmentCloneSchemaCoverage{}, err
	}
	tables := make([]ProjectEnvironmentCloneSchemaPolicy, 0, len(rows))
	for _, row := range rows {
		tables = append(tables, ProjectEnvironmentCloneSchemaPolicy{TableName: row.TableName, Columns: row.Columns})
	}
	return cloneSchemaCoverage(tables)
}

// This report is scoped before inspecting metadata and never reads tenant
// row values. It inventories the application schema boundary. Source resource
// instances and their content still need separate frozen captures.
func (s *PgStore) ProjectEnvironmentCloneSchemaCoverage(ctx context.Context, accountID, projectID string) (ProjectEnvironmentCloneSchemaCoverage, error) {
	if !validCloneCredentialSourceID(accountID) || !validCloneCredentialSourceID(projectID) {
		return ProjectEnvironmentCloneSchemaCoverage{}, ErrNotFound
	}
	project, err := s.ProjectByID(ctx, projectID)
	if err != nil {
		return ProjectEnvironmentCloneSchemaCoverage{}, err
	}
	if project.AccountID != accountID {
		return ProjectEnvironmentCloneSchemaCoverage{}, ErrNotFound
	}
	return readCloneSchemaCoverageDB(ctx, s.pool)
}

var _ ProjectEnvironmentCloneSchemaCoverageStore = (*PgStore)(nil)
