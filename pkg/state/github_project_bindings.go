package state

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// GitHubProjectBindingsForInstallation includes projects whose last app was
// removed. A subsequent push must still reconcile newly added workloads.
func (s *PgStore) GitHubProjectBindingsForInstallation(ctx context.Context, installationID int64) ([]GitHubBinding, error) {
	rows, err := sqlc.New().GitHubProjectBindingsForInstallation(ctx, s.pool, pgtype.Int8{Int64: installationID, Valid: true})
	if err != nil {
		return nil, err
	}
	bindings := make([]GitHubBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, GitHubBinding{BindingID: "project-" + row.ProjectID, AccountID: row.AccountID,
			InstallID: installationID, RepoFullName: row.RepoFullName, ProductionBranch: row.ProductionBranch})
	}
	return bindings, nil
}
