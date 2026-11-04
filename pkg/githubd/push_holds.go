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
	env, err := reposcan.ReadProjectEnv(fsys, s.scanOptions(project.AccountID), scan.Workloads)
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
