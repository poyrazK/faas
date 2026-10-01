package state

import (
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func projectSQLParams(p Project) sqlc.CreateProjectWithComputeRegionParams {
	return sqlc.CreateProjectWithComputeRegionParams{
		AccountID: uuidToPgtype(p.AccountID), Slug: p.Slug, RepoFullName: pgtype.Text{String: p.RepoFullName, Valid: p.RepoFullName != ""},
		ProductionBranch: pgtype.Text{String: p.ProductionBranch, Valid: p.ProductionBranch != ""}, InstallID: pgtype.Int8{Int64: p.InstallID, Valid: true},
		ScanSource: string(p.ScanSource), ComputeRegion: p.ComputeRegion,
	}
}

func projectFromSQL(p sqlc.Project) Project {
	return Project{ID: uuidString(p.ID), AccountID: uuidString(p.AccountID), Slug: p.Slug,
		RepoFullName: p.RepoFullName.String, ProductionBranch: p.ProductionBranch.String,
		InstallID: p.InstallID.Int64, ScanSource: ProjectScanSource(p.ScanSource), ComputeRegion: p.ComputeRegion,
		CreatedAt: p.CreatedAt.Time, UpdatedAt: p.UpdatedAt.Time}
}
