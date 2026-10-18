package state

import (
	"context"

	ownersql "github.com/onebox-faas/faas/pkg/state/resourceownership/sqlc"
)

func ownedProject(row ownersql.Project) Project {
	return Project{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID),
		OrgID: pgUUIDString(row.OrgID), Slug: row.Slug, RepoFullName: row.RepoFullName.String,
		ProductionBranch: row.ProductionBranch.String, InstallID: row.InstallID.Int64,
		ScanSource: ProjectScanSource(row.ScanSource), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func (s *PgStore) ProjectByOrgSlug(ctx context.Context, orgID, slug string) (Project, error) {
	row, err := ownersql.New().ProjectByOrgSlug(ctx, s.pool, ownersql.ProjectByOrgSlugParams{OrgID: mustPgUUID(orgID), Slug: slug})
	if err != nil {
		return Project{}, mapErr(err)
	}
	return ownedProject(row), nil
}

func (s *PgStore) ListProjectsForOrg(ctx context.Context, orgID string) ([]Project, error) {
	rows, err := ownersql.New().ListOrgProjects(ctx, s.pool, mustPgUUID(orgID))
	if err != nil {
		return nil, mapErr(err)
	}
	projects := make([]Project, 0, len(rows))
	for _, row := range rows {
		projects = append(projects, ownedProject(row))
	}
	return projects, nil
}
