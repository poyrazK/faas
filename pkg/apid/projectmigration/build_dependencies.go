package projectmigration

import (
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateBuildDependencies(in api.CreateProjectMigrationRequest, plan api.PlanResponse) error {
	for caller, targets := range in.BuildDependencies {
		i := slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == caller })
		if i < 0 || !slices.Contains(in.Only, caller) || len(targets) > api.ProjectMigrationMaxServices {
			return api.ErrValidation("build dependencies must belong to selected migration services")
		}
		seen := map[string]bool{}
		for _, target := range targets {
			if target == caller || seen[target] || !slices.Contains(in.Only, target) || !slices.Contains(plan.Workloads[i].DependsOn, target) {
				return api.ErrValidation("choose each selected, declared API dependency once for a build wait")
			}
			seen[target] = true
		}
	}
	return nil
}

func (r *Run) validateSavedBuildDependencies(workloads []reposcan.Workload) error {
	for _, s := range r.record.Report.Services {
		if !r.targets[s.Name] {
			continue
		}
		i := slices.IndexFunc(workloads, func(w reposcan.Workload) bool { return w.Name == s.Name })
		if i < 0 {
			return state.ErrConflict
		}
		for _, target := range s.BuildDependencies {
			if !slices.Contains(workloads[i].DependsOn, target) {
				return api.ErrValidation("a saved build dependency is no longer declared in the original source")
			}
		}
	}
	return nil
}

// BuildDependencies resolves only journaled attempt identities. In particular,
// a failed admission cannot make a caller silently adopt an older serving API.
func (r *Run) BuildDependencies(appID string) []state.DeploymentDependencyPin {
	var out []state.DeploymentDependencyPin
	for _, s := range r.record.Report.Services {
		if s.AppID != appID || !r.targets[s.Name] {
			continue
		}
		for _, name := range s.BuildDependencies {
			for _, target := range r.record.Report.Services {
				if target.Name == name {
					out = append(out, state.DeploymentDependencyPin{Service: name, AppID: target.AppID, DeploymentID: target.DeploymentID})
				}
			}
		}
	}
	return out
}
