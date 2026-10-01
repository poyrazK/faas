package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) resolveProjectComputeRegion(ctx context.Context, acct state.Account, req *scanPlanRequest) *api.Problem {
	project, err := s.store.ProjectBySlug(ctx, acct.ID, req.ProjectSlug)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	if err != nil {
		return api.ErrInternal("Could not load project compute region")
	}
	if req.ComputeRegion != "" && req.ComputeRegion != project.ComputeRegion {
		p := api.NewProblem(http.StatusConflict, api.CodeConflict, "Immutable project compute region",
			fmt.Sprintf("Project %q has immutable default compute region %q; create a new project to use %q.", req.ProjectSlug, project.ComputeRegion, req.ComputeRegion))
		p.ComputeRegion = req.ComputeRegion
		return p
	}
	req.ComputeRegion = project.ComputeRegion
	return nil
}

func (s *server) projectNewWorkloadRegionProblem(ctx context.Context, workloads []reposcan.Workload, existing []state.App, project state.Project) *api.Problem {
	if project.ID == "" && project.ComputeRegion != "" {
		if p := s.validateComputeRegionAvailable(ctx, project.ComputeRegion); p != nil {
			return p
		}
	}
	members := make(map[string]bool)
	for _, app := range existing {
		if project.ID != "" && app.ProjectID == project.ID && app.WorkloadName != "" && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			members[app.WorkloadName] = true
		}
	}
	checked := make(map[string]bool)
	for _, workload := range workloads {
		if members[workload.Name] || workload.ComputeRegion == "" || checked[workload.ComputeRegion] {
			continue
		}
		if prob := s.validateComputeRegionAvailable(ctx, workload.ComputeRegion); prob != nil {
			return prob
		}
		checked[workload.ComputeRegion] = true
	}
	return nil
}
