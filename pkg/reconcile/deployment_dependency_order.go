package reconcile

import (
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// OrderProjectDeploymentApps orders the selected admission set, including
// changed members returned in store order by GitHub push reconciliation.
func OrderProjectDeploymentApps(apps []state.App) ([]state.App, error) {
	hasGate := false
	for _, app := range apps {
		for _, condition := range app.Manifest.ProjectDependencyConditions {
			hasGate = hasGate || condition == api.ComposeDependencyHealthy
		}
	}
	if !hasGate {
		return append([]state.App(nil), apps...), nil
	}
	selected := ProjectDeploymentSelection(apps)
	workloads := make([]reposcan.Workload, 0, len(apps))
	for _, app := range apps {
		workload := reposcan.Workload{Name: app.WorkloadName}
		for _, binding := range app.Manifest.ServiceBindings {
			if selected[strings.ToLower(binding.Service)] {
				workload.DependsOn = append(workload.DependsOn, binding.Service)
			}
		}
		workloads = append(workloads, workload)
	}
	order, err := reposcan.DependencyOrder(workloads, nil)
	if err != nil {
		return nil, err
	}
	position := make(map[string]int, len(order))
	for i, name := range order {
		position[strings.ToLower(name)] = i
	}
	out := append([]state.App(nil), apps...)
	sort.SliceStable(out, func(i, j int) bool {
		return position[strings.ToLower(out[i].WorkloadName)] < position[strings.ToLower(out[j].WorkloadName)]
	})
	return out, nil
}

func ProjectDeploymentSelection(apps []state.App) map[string]bool {
	selected := make(map[string]bool, len(apps))
	for _, app := range apps {
		selected[strings.ToLower(app.WorkloadName)] = true
	}
	return selected
}

// ProjectDependencyAdmissionBlocker prevents a failed selected dependency from
// falling back to an older serving release during a partially successful apply.
func ProjectDependencyAdmissionBlocker(app state.App, selected, accepted map[string]bool) string {
	names := make([]string, 0, len(app.Manifest.ProjectDependencyConditions))
	for name, condition := range app.Manifest.ProjectDependencyConditions {
		if condition == api.ComposeDependencyHealthy && selected[name] && !accepted[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("dependency %q was selected but its deployment was not accepted; dependent release was not admitted", names[0])
}
