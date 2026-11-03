package githubd

import (
	"context"
	"fmt"
	"io/fs"
	"sort"

	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// pushKeptServices lists the project's apps whose service is not in the scan
// and was not renamed: the apps a reconcile would remove. A push keeps them
// (ADR-20261103112845). Removing a service deletes its app with its domains,
// variables and crons, and a push applies with nobody reviewing the plan; a
// service also leaves the scan when an inactive Compose profile hides it.
func (s *Service) pushKeptServices(ctx context.Context, project state.Project, scan reposcan.Result) ([]state.App, error) {
	apps, err := s.Reconcile.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list project apps for removals: %w", err)
	}
	scanned := make(map[string]bool, len(scan.Workloads))
	for _, w := range scan.Workloads {
		scanned[w.Name] = true
	}
	renamed := reconcile.RenamedWorkloads(scan.Workloads, apps)
	var kept []state.App
	for _, app := range apps {
		if app.Status == state.AppDeleted || app.PreviewOfSlug != "" || app.WorkloadName == "" {
			continue
		}
		if scanned[app.WorkloadName] || renamed[app.WorkloadName] != "" {
			continue
		}
		kept = append(kept, app)
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].WorkloadName < kept[j].WorkloadName })
	return kept, nil
}

// pushServicesWaitingForDatabase lists the project's apps that expect a
// Compose postgres service's address (DATABASE_URL: postgres://…@db/…, or
// depends_on: [db]) and have no value for that variable in scope. A reviewed
// deploy creates and binds the managed database for them (ADR-20261025111824);
// a push cannot, so they are not deployed by it (ADR-20261103112845) rather
// than starting without their database.
func (s *Service) pushServicesWaitingForDatabase(ctx context.Context, project state.Project, scan reposcan.Result, fsys fs.FS, scope string) (map[string]string, error) {
	var postgres []reposcan.Managed
	for _, m := range scan.Managed {
		if m.Kind == "postgres" {
			postgres = append(postgres, m)
		}
	}
	if len(postgres) == 0 {
		return nil, nil
	}
	env, err := reposcan.ReadProjectEnv(fsys, s.scanOptions(project.AccountID))
	if err != nil {
		return nil, fmt.Errorf("githubd: read compose environment: %w", err)
	}
	apps, err := s.Reconcile.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list project apps for databases: %w", err)
	}
	byWorkload := make(map[string]state.App, len(apps))
	for _, app := range apps {
		if app.Status != state.AppDeleted && app.PreviewOfSlug == "" {
			byWorkload[app.WorkloadName] = app
		}
	}
	waiting := map[string]string{}
	for _, m := range postgres {
		for _, consumer := range projectsetup.ManagedConsumers(env, scan.Workloads, m.Name, m.EnvHint) {
			app, ok := byWorkload[consumer.Workload]
			if !ok {
				continue
			}
			if _, already := waiting[app.ID]; already {
				continue
			}
			unset, err := projectsetup.UnsetEnvKeys(ctx, s.Reconcile.Store, project.AccountID, scope, &app, []string{consumer.EnvKey})
			if err != nil {
				return nil, fmt.Errorf("githubd: read %s configuration: %w", app.Slug, err)
			}
			if len(unset) == 0 {
				continue
			}
			waiting[app.ID] = fmt.Sprintf("Not deployed: %s needs %s for the Compose postgres service %q, which a GitHub push cannot create. Deploy with `gregale deploy` or from the dashboard to create and bind the managed database, or set %s with `gregale env set --app %s`.",
				consumer.Workload, consumer.EnvKey, m.Name, consumer.EnvKey, app.Slug)
		}
	}
	return waiting, nil
}

// pushServicesWithHeldSlugs moves out of scan.Workloads every new service
// whose app slug another app still holds, and returns the held services'
// check reasons by slug. apps.slug is unique and a deleted app keeps its slug
// through the restore window, so creating the service's app failed the whole
// push reconcile (apps_slug_key) and no other service deployed. The held
// services stay in scan.DependencyReferences, so their siblings' dependency
// declarations still validate; the reviewed plan explains the same conflict
// (reconcile.SlugHolderReasons).
func (s *Service) pushServicesWithHeldSlugs(ctx context.Context, project state.Project, scan *reposcan.Result) (map[string]string, error) {
	accountApps, err := s.Reconcile.Store.ListApps(ctx, project.AccountID)
	if err != nil {
		return nil, fmt.Errorf("githubd: list account apps for slugs: %w", err)
	}
	if project.OrgID != "" {
		visible := accountApps[:0]
		for _, app := range accountApps {
			if app.OrgID == project.OrgID {
				visible = append(visible, app)
			}
		}
		accountApps = visible
	}
	lookup := s.Reconcile.Store.AppBySlugIncludingDeleted
	allocated, err := reconcile.AllocateAppSlugs(ctx, lookup, scan.Workloads, accountApps, project.AccountID, project.ID, project.Slug)
	if err != nil {
		return nil, fmt.Errorf("githubd: allocate app slugs: %w", err)
	}
	holders, err := reconcile.SlugHolders(ctx, lookup, allocated, accountApps)
	if err != nil {
		return nil, fmt.Errorf("githubd: look up held slugs: %w", err)
	}
	if len(holders) == 0 {
		return nil, nil
	}
	reasons := map[string]string{}
	var kept []reposcan.Workload
	for i, workload := range allocated {
		slug := reconcile.AppSlug(workload)
		holder, held := holders[slug]
		if !held {
			kept = append(kept, scan.Workloads[i])
			continue
		}
		scan.DependencyReferences = append(scan.DependencyReferences, scan.Workloads[i])
		if holder.AccountID == project.AccountID && holder.Status == state.AppDeleted {
			reasons[slug] = fmt.Sprintf("Not deployed: %s would create app %s, but a deleted app keeps that name during its restore window. Restore it with `gregale app restore %s`, or give the service another app name.", workload.Name, slug, slug)
		} else {
			reasons[slug] = fmt.Sprintf("Not deployed: %s would create app %s, but another app already uses that name. Give the service another app name.", workload.Name, slug)
		}
	}
	scan.Workloads = kept
	return reasons, nil
}
