package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnvironmentWorkloadAdmissionReasons rejects a rename of shared identity.
// A stage may add services, but only a production plan may rename an app.
func EnvironmentWorkloadAdmissionReasons(workloads []reposcan.Workload, existing []state.App) []string {
	if len(RenamedWorkloads(workloads, existing)) != 0 {
		return []string{"rename project services in production before deploying them to another environment"}
	}
	return nil
}

// ReconcileEnvironment prepares selected services in a registered stage. App
// identity is shared; existing app configuration and omitted apps are left to
// production reconciliation. Each selected workload gets an immutable scoped
// settings revision, which CreateDeployment pins before workers can read it.
// The caller must authorize the exact plan for a protected environment.
func (s *Service) ReconcileEnvironment(ctx context.Context, project state.Project, scan reposcan.Result, environment, commitSHA string, approvedProtected bool) (Result, error) {
	var out Result
	if state.IsProductionScope(environment) || !api.ValidProjectEnvironmentSlug(environment) || len(scan.Workloads) == 0 {
		return out, state.ErrInvalidArgument
	}
	env, err := s.Store.ProjectEnvironmentBySlug(ctx, project.AccountID, project.ID, environment)
	if err != nil {
		return out, err
	}
	if env.Protected && !approvedProtected {
		return out, state.ErrConflict
	}
	specs, ok := s.Store.(state.ProjectEnvironmentWorkloadSpecStore)
	if !ok {
		return out, fmt.Errorf("reconcile: environment workload settings unavailable")
	}
	mutator, ok := s.Store.(state.ProjectReconcileStore)
	if !ok {
		return out, fmt.Errorf("reconcile: atomic project membership unavailable")
	}
	existing, err := s.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return out, err
	}
	if reasons := EnvironmentWorkloadAdmissionReasons(scan.Workloads, existing); len(reasons) != 0 {
		return out, state.ErrInvalidArgument
	}
	accountApps, err := s.Store.ListApps(ctx, project.AccountID)
	if err != nil {
		return out, err
	}
	scan.Workloads, err = AllocateAppSlugs(ctx, s.Store.AppBySlugIncludingDeleted, scan.Workloads, accountApps, project.AccountID, project.ID, project.Slug)
	if err != nil {
		return out, err
	}
	if err := validateWorkloadAdmissionWithManaged(scan.Workloads, scan.Managed, accountApps, project.ID, scan.DependencyReferences); err != nil {
		return out, err
	}
	account, err := state.ProjectResourceAccount(ctx, s.Store, project)
	if err != nil {
		return out, err
	}
	if err := validatePlatformTenantPolicy(account.Plan, scan.Workloads); err != nil {
		return out, err
	}
	available := availableServiceNames(scan, project, existing)
	byName := make(map[string]state.App, len(existing))
	for _, app := range existing {
		if app.PreviewOfSlug == "" && app.Status != state.AppDeleted {
			byName[app.WorkloadName] = app
		}
	}
	var creates []state.ProjectReconcileMutation
	for _, w := range scan.Workloads {
		if _, found := byName[w.Name]; !found {
			app := workloadToDraftApp(project, w, resolveStartCommand(w), account.Plan, available)
			creates = append(creates, state.ProjectReconcileMutation{Op: "create", App: app,
				SetPlatformTenantRequired: w.PlatformTenantRequired != nil, SetResources: !w.Resources.IsZero()})
		}
	}
	added := make(map[string]bool, len(creates))
	if len(creates) > 0 {
		// This transaction creates identities only. It must never update or
		// delete existing apps, replace crons, or change the production source.
		applied, err := mutator.ApplyProjectReconcile(ctx, project, creates, nil, project.ScanSource, s.Limits(account.Plan))
		if err != nil {
			return out, err
		}
		for _, app := range applied.Added {
			byName[app.WorkloadName], added[app.ID] = app, true
			s.emitWorkloadAdded(ctx, project, app, commitSHA)
		}
		s.labelAddedApps(ctx, applied.Added, scan.Workloads)
		if err := s.syncAddedAppProtocols(ctx, applied.Added, scan.Workloads); err != nil {
			return out, err
		}
	}
	for _, w := range scan.Workloads {
		app, found := byName[w.Name]
		if !found {
			return out, state.ErrConflict
		}
		staged, err := stageEnvironmentWorkload(ctx, specs, s.Store, project, env, app, w, available, approvedProtected)
		if err != nil {
			return out, fmt.Errorf("reconcile: stage workload %q: %w", w.Name, err)
		}
		if added[app.ID] {
			out.Added = append(out.Added, staged)
		} else {
			out.Changed = append(out.Changed, staged)
		}
	}
	return out, nil
}

func stageEnvironmentWorkload(ctx context.Context, specs state.ProjectEnvironmentWorkloadSpecStore, store state.Store, project state.Project, environment state.ProjectEnvironment, app state.App, w reposcan.Workload, available map[string]struct{}, approvedProtected bool) (state.App, error) {
	current, err := specs.ProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, environment.Slug, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		current.Settings, err = state.MaterializeEnvironmentWorkloadSettings(ctx, store, app, environment.Slug)
	}
	if err != nil {
		return state.App{}, err
	}
	if current.ID != "" {
		hash, err := state.WorkloadSettingsHash(current.Settings)
		if err != nil || hash != current.Hash {
			return state.App{}, state.ErrConflict
		}
	}
	base, err := current.Settings.ApplyTo(app)
	if err != nil {
		return state.App{}, err
	}
	desired := ApplyScannedWorkloadToApp(base, keepServingClass(w, base), available)
	settings, err := state.WorkloadSettingsFromApp(desired)
	if err != nil {
		return state.App{}, err
	}
	settings.WorkPolicies, settings.QueueBindings = current.Settings.WorkPolicies, current.Settings.QueueBindings
	settings.Manifest.ProjectSourceSHA256 = w.SourceSHA256
	var spec state.ProjectEnvironmentWorkloadSpec
	if approvedProtected {
		spec, err = specs.PutProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, environment.Slug, app.ID, current.Revision, settings)
	} else {
		// Protection can change after the request's initial read. The store
		// rechecks it under the same lock that advances the settings head.
		spec, err = specs.PutUnprotectedProjectEnvironmentWorkloadSpec(ctx, project.AccountID, project.ID, environment.ID, app.ID, current.Revision, settings)
	}
	if err != nil {
		return state.App{}, err
	}
	return spec.Settings.ApplyTo(app)
}
