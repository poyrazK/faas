package projectaccess

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type ProjectInventory interface {
	state.ProjectOwnershipStore
	AppsForProject(context.Context, string, string) ([]state.App, error)
}

func ListProjects(ctx context.Context, store ProjectInventory) ([]api.ProjectSummaryResponse, error) {
	grant, ok := From(ctx)
	if !ok {
		return nil, errors.New("organization project grant missing")
	}
	projects, err := store.ListProjectsForOrg(ctx, grant.Org.ID)
	if err != nil {
		return nil, err
	}
	result := make([]api.ProjectSummaryResponse, 0, len(projects))
	for _, project := range projects {
		apps, err := store.AppsForProject(ctx, project.AccountID, project.ID)
		if err != nil {
			return nil, err
		}
		result = append(result, Summary(project, len(apps)))
	}
	return result, nil
}

func Summary(project state.Project, workloads int) api.ProjectSummaryResponse {
	return api.ProjectSummaryResponse{ID: project.ID, OrgID: project.OrgID, Slug: project.Slug,
		RepoFullName: project.RepoFullName, ProductionBranch: project.ProductionBranch,
		ScanSource: string(project.ScanSource), WorkloadCount: workloads,
		CreatedAt: project.CreatedAt.UTC().Format(time.RFC3339), UpdatedAt: project.UpdatedAt.UTC().Format(time.RFC3339)}
}
