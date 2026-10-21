package projectmigration

import (
	"maps"
	"slices"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateConnections(in api.CreateProjectMigrationRequest, plan api.PlanResponse) error {
	for caller, connections := range in.ServiceConnections {
		i := slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == caller })
		if i < 0 || !slices.Contains(in.Only, caller) || len(connections) > api.ProjectMigrationMaxServiceConnections {
			return api.ErrValidation("connections must target a selected caller and contain at most ten runtime variables")
		}
		for key, target := range connections {
			if problem := api.ValidateMigrationConnectionKey(key); problem != nil {
				return problem
			}
			if target == caller || !slices.Contains(in.Only, target) || !slices.Contains(plan.Workloads[i].DependsOn, target) {
				return api.ErrValidation("connect only to a declared HTTP or GraphQL dependency selected in this migration")
			}
			names, err := api.NormalizeServiceBindingTargets([]string{target})
			if err != nil || len(names) != 1 || names[0] != target {
				return api.ErrValidation("connection targets must have canonical internal service names")
			}
			if _, present := in.InitialSecrets[caller][key]; present {
				return api.ErrValidation("a runtime key cannot have both a service connection and a supplied value")
			}
		}
	}
	return nil
}

func (r *Run) validateConnectionValues(values api.ProjectInitialSecrets) error {
	for _, s := range r.record.Report.Services {
		for key := range s.PublicBuildURLs {
			if _, present := values[s.Name][key]; present {
				return api.ErrValidation("saved public build URLs cannot be replaced by supplied secret values")
			}
		}
		for key := range s.ServiceConnections {
			if _, present := values[s.Name][key]; present {
				return api.ErrValidation("saved service connections cannot be replaced by supplied configuration values")
			}
		}
	}
	return nil
}

// InitialConfiguration reconstructs saved connection choices for unfinished
// callers only. URLs use the existing HTTPS binding and stable logical
// service names, so later deployments keep working without repository edits.
// The normal configuration owner seals these values before any build starts.
func (r *Run) InitialConfiguration(values api.ProjectInitialSecrets, workloads []reposcan.Workload) (api.ProjectInitialSecrets, error) {
	if err := r.ValidatePublicConfiguration(workloads); err != nil {
		return nil, err
	}
	if err := r.validateConnectionValues(values); err != nil {
		return nil, err
	}
	out := api.ProjectInitialSecrets{}
	for name, entries := range values {
		out[name] = maps.Clone(entries)
	}
	for _, s := range r.record.Report.Services {
		if !r.targets[s.Name] || len(s.ServiceConnections) == 0 {
			continue
		}
		i := slices.IndexFunc(workloads, func(w reposcan.Workload) bool { return w.Name == s.Name })
		if i < 0 {
			return nil, state.ErrConflict
		}
		w := workloads[i]
		if out[s.Name] == nil {
			out[s.Name] = map[string]string{}
		}
		for key, target := range s.ServiceConnections {
			if !slices.Contains(w.DependsOn, target) {
				return nil, api.ErrValidation("the saved connection is no longer a declared service dependency")
			}
			bindings := api.ServiceBindingsForTargets([]string{target})
			env := api.ServiceBindingEnvForTransport(nil, bindings, api.ServiceBindingTransportHTTPS)
			out[s.Name][key] = env[api.ServiceBindingEnvKey(target)]
		}
	}
	return out, nil
}

func configuredKeys(values map[string]string, connections map[string]string, public ...map[string]string) []string {
	keys := map[string]bool{}
	for key := range values {
		keys[key] = true
	}
	for key := range connections {
		keys[key] = true
	}
	for _, urls := range public {
		for key := range urls {
			keys[key] = true
		}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
