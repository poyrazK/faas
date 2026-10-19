package projectmigration

import (
	"context"
	"maps"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func publicURLs(in api.CreateProjectMigrationRequest, plan api.PlanResponse) (api.ProjectPublicBuildURLs, error) {
	out := api.ProjectPublicBuildURLs{}
	for caller, urls := range in.PublicBuildURLs {
		if !slices.Contains(in.Only, caller) {
			return nil, api.ErrValidation("public build URLs must target selected services")
		}
		out[caller] = maps.Clone(urls)
	}
	for caller, connections := range in.PublicServiceConnections {
		i := slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == caller })
		if i < 0 || !slices.Contains(in.Only, caller) || len(connections) > api.PublicBuildURLsMaxPerApp {
			return nil, api.ErrValidation("public connections must target a selected caller and at most ten public URL variables")
		}
		if out[caller] == nil {
			out[caller] = map[string]string{}
		}
		for key, target := range connections {
			j := slices.IndexFunc(plan.Workloads, func(w api.PlanWorkload) bool { return w.Name == target })
			if target == caller || j < 0 || !slices.Contains(in.Only, target) || !slices.Contains(plan.Workloads[i].DependsOn, target) {
				return nil, api.ErrValidation("public connections require a selected declared HTTP or GraphQL dependency")
			}
			if _, exists := out[caller][key]; exists {
				return nil, api.ErrValidation("choose either a public service connection or an explicit public URL for each key")
			}
			out[caller][key] = plan.Workloads[j].PublicURL
		}
	}
	for caller, urls := range out {
		if problem := api.ValidatePublicBuildURLs(urls); problem != nil {
			return nil, problem
		}
		for key := range urls {
			if _, exists := in.InitialSecrets[caller][key]; exists {
				return nil, api.ErrValidation("a public build URL cannot also be supplied as a secret")
			}
			if _, exists := in.ServiceConnections[caller][key]; exists {
				return nil, api.ErrValidation("a public build URL cannot also be a private runtime connection")
			}
		}
	}
	return out, nil
}

// ValidatePublicConfiguration rechecks saved dependencies against the exact
// source before reconcile. A retry retains the reviewed public URL values.
func (r *Run) ValidatePublicConfiguration(workloads []reposcan.Workload) error {
	if err := r.validateSavedBuildDependencies(workloads); err != nil {
		return err
	}
	for _, s := range r.record.Report.Services {
		if !r.targets[s.Name] {
			continue
		}
		i := slices.IndexFunc(workloads, func(w reposcan.Workload) bool { return w.Name == s.Name })
		if i < 0 {
			return state.ErrConflict
		}
		for _, target := range s.PublicServiceConnections {
			if !slices.Contains(workloads[i].DependsOn, target) {
				return api.ErrValidation("a saved public connection is no longer a declared dependency")
			}
		}
	}
	return nil
}

func (r *Run) configurePublicURLs(ctx context.Context, project string, apps []state.App) error {
	var writes []struct {
		app  state.App
		urls map[string]string
	}
	for _, s := range r.record.Report.Services {
		if !r.targets[s.Name] || len(s.PublicBuildURLs) == 0 {
			continue
		}
		i := slices.IndexFunc(apps, func(a state.App) bool {
			return a.AccountID == r.backend.AccountID && a.ProjectID == project && a.WorkloadName == s.Name && a.Slug == s.App && a.Status != state.AppDeleted
		})
		if i < 0 {
			return state.ErrConflict
		}
		merged := maps.Clone(apps[i].Manifest.PublicBuildURLs)
		if merged == nil {
			merged = map[string]string{}
		}
		maps.Copy(merged, s.PublicBuildURLs)
		if problem := api.ValidatePublicBuildURLs(merged); problem != nil {
			return problem
		}
		if err := r.checkPublicRuntimeCollisions(ctx, apps[i], s.PublicBuildURLs); err != nil {
			return err
		}
		writes = append(writes, struct {
			app  state.App
			urls map[string]string
		}{apps[i], merged})
	}
	if len(writes) == 0 {
		return nil
	}
	store, ok := r.backend.Store.(state.PublicBuildURLStore)
	if !ok {
		return api.ErrCapacity("public build configuration is unavailable")
	}
	for _, write := range writes {
		if err := store.SetAppPublicBuildURLs(ctx, r.backend.AccountID, write.app.ID, write.urls); err != nil {
			return err
		}
	}
	return nil
}

func (r *Run) checkPublicRuntimeCollisions(ctx context.Context, app state.App, urls map[string]string) error {
	reader, ok := r.backend.Reader.(interface {
		ListAllAppSecrets(context.Context, string, string) ([]state.AppSecret, error)
		ListAllAppEnv(context.Context, string, string) ([]state.AppEnv, error)
	})
	if !ok {
		return api.ErrCapacity("runtime configuration metadata is unavailable")
	}
	secrets, err := reader.ListAllAppSecrets(ctx, r.backend.AccountID, app.ID)
	if err != nil {
		return err
	}
	for _, secret := range secrets {
		if _, exists := urls[secret.Key]; exists && secret.Scope == api.DefaultEnvScope {
			return api.ErrValidation("remove the existing runtime secret for " + secret.Key + " before configuring it as a public build URL")
		}
	}
	env, err := reader.ListAllAppEnv(ctx, r.backend.AccountID, app.ID)
	if err != nil {
		return err
	}
	for _, row := range env {
		if value, exists := urls[row.Key]; exists && row.Scope == api.DefaultEnvScope && row.Value != value {
			return api.ErrValidation("update or remove the existing runtime variable for " + row.Key + " before configuring its public build URL")
		}
	}
	if app.Manifest.Env != nil {
		for key, value := range urls {
			if existing, ok := app.Manifest.Env[key]; ok && existing != value {
				return api.ErrValidation("update or remove the existing manifest runtime variable for " + key + " before configuring its public build URL")
			}
		}
	}
	return nil
}
