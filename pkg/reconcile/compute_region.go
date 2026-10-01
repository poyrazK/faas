package reconcile

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// WorkloadsWithComputeRegions resolves omission without repinning existing
// members. Project defaults apply only to new members. Explicit declarations
// remain intact so admission can reject immutable-region conflicts.
func WorkloadsWithComputeRegions(workloads []reposcan.Workload, existing []state.App, project state.Project) []reposcan.Workload {
	byName := make(map[string]string, len(existing))
	for _, app := range existing {
		if app.ProjectID == project.ID && project.ID != "" && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byName[app.WorkloadName] = app.ComputeRegion
		}
	}
	out := append([]reposcan.Workload(nil), workloads...)
	for i := range out {
		if out[i].ComputeRegion != "" {
			continue
		}
		if region, exists := byName[out[i].Name]; exists {
			out[i].ComputeRegion = region
		} else {
			out[i].ComputeRegion = project.ComputeRegion
		}
	}
	return out
}

func (s *Service) validateNewWorkloadRegions(ctx context.Context, workloads []reposcan.Workload, existing []state.App, project state.Project) error {
	members := make(map[string]bool)
	for _, app := range existing {
		if project.ID != "" && app.ProjectID == project.ID && app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			members[app.WorkloadName] = true
		}
	}
	checked := make(map[string]bool)
	for _, workload := range workloads {
		if workload.ComputeRegion == "" || members[workload.Name] || checked[workload.ComputeRegion] {
			continue
		}
		if s.ValidateNewComputeRegion == nil {
			return &api.APIError{Problem: *state.ValidateNewComputeRegion(ctx, s.Store, false, workload.ComputeRegion)}
		}
		if err := s.ValidateNewComputeRegion(ctx, workload.ComputeRegion); err != nil {
			return err
		}
		checked[workload.ComputeRegion] = true
	}
	return nil
}
