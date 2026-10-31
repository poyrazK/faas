package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// validateProjectManifestPathRoutes checks route targets before reconcile
// writes any project/app state (ADR-20261021223225). A route may name a
// current project app slug or the workload that creates it; filtered
// workloads remain valid declarations and are dormant for this apply.
func validateProjectManifestPathRoutes(
	routes []gregalemanifest.PathRoute,
	allWorkloads, selectedWorkloads []reposcan.Workload,
	projectApps []state.App,
	excluded map[string]bool,
) *api.Problem {
	for i, route := range routes {
		workload, app, found := projectAsyncRouteTarget(route.To, allWorkloads, projectApps)
		if !found {
			return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("routes[%d].to %q does not name a workload in this project", i, route.To))
		}
		if workload.Name == "" {
			if excluded[strings.ToLower(route.To)] || (app != nil && appMatchesSelectors(excluded, *app)) {
				continue
			}
			return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("routes[%d].to %q does not name a workload in this project source; exclude it explicitly to retain it", i, route.To))
		}
		if !projectWorkloadSelected(workload, selectedWorkloads) {
			continue
		}
		class := projectWorkloadClassFromScan(workload)
		probe := state.App{WorkloadClass: class}
		if app != nil {
			probe.Manifest.ExecutionMode = app.Manifest.ExecutionMode
		}
		if !probe.AcceptsRequestInvocations() {
			return api.ErrInvocationWorkloadClass(string(class), probe.Manifest.ExecutionMode)
		}
	}
	return nil
}

// applyProjectManifestPathRoutes reconciles routes on the selected project
// apps. A declared list clears manifest-owned routes on selected apps it no
// longer names; unselected apps keep theirs.
func (s *server) applyProjectManifestPathRoutes(
	ctx context.Context,
	acct state.Account,
	routes []gregalemanifest.PathRoute,
	present bool,
	noTriggers bool,
	selectedApps []state.App,
) *api.Problem {
	if !present || noTriggers {
		return nil
	}
	apps := append([]state.App(nil), selectedApps...)
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Slug != apps[j].Slug {
			return apps[i].Slug < apps[j].Slug
		}
		return apps[i].ID < apps[j].ID
	})
	var staged []sourceRefManifestStaged
	rollback := func() {
		for i := len(staged) - 1; i >= 0; i-- {
			if err := s.rollbackSourceRefManifest(context.WithoutCancel(ctx), staged[i]); err != nil && s.log != nil {
				s.log.Warn("project route rollback incomplete", "app_id", staged[i].appID, "err", err)
			}
		}
	}
	for _, app := range apps {
		change := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
		problem := s.applySourceRefManifestPathRoutes(ctx, acct, app, projectPathRoutesForApp(routes, app), &change)
		if len(change.edgeRuleChanges) > 0 {
			staged = append(staged, change)
		}
		if problem != nil {
			rollback()
			return problem
		}
	}
	return nil
}

// projectPathRoutesForApp returns the routes that target app, by slug or
// workload name, rewritten to the slug. The result is never nil, so an app
// the manifest no longer routes to has its manifest-owned routes cleared.
func projectPathRoutesForApp(routes []gregalemanifest.PathRoute, app state.App) []gregalemanifest.PathRoute {
	matching := make([]gregalemanifest.PathRoute, 0)
	for _, route := range routes {
		if route.To != app.Slug && route.To != app.WorkloadName {
			continue
		}
		route.To = app.Slug
		matching = append(matching, route)
	}
	return matching
}

func projectPathRoutePlanWarning(routes []gregalemanifest.PathRoute, present, noTriggers bool) string {
	if !present {
		return ""
	}
	if noTriggers {
		return "routes skipped by request (--no-triggers); existing routes left unchanged"
	}
	if len(routes) == 0 {
		return "routes: [] clears manifest-owned routes on selected project workloads"
	}
	return fmt.Sprintf("%d route declaration(s) will reconcile across selected project workloads; omitted routes on those workloads are cleared", len(routes))
}
