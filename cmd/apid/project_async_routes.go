package main

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

// validateProjectManifestAsyncRoutes verifies targets before reconcile writes
// any project/app state. A route may name a current project app slug or the
// workload name that creates it; filtered workloads remain valid declarations
// and are dormant for this apply.
func validateProjectManifestAsyncRoutes(
	routes []gregalemanifest.AsyncRoute,
	allWorkloads, selectedWorkloads []reposcan.Workload,
	projectApps []state.App,
	excluded map[string]bool,
) *api.Problem {
	if len(routes) == 0 {
		return nil
	}
	for i, route := range routes {
		workload, app, found := projectAsyncRouteTarget(route.App, allWorkloads, projectApps)
		if !found {
			return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("async_routes[%d].app %q does not name a workload in this project", i, route.App))
		}
		if workload.Name == "" {
			// Existing workloads removed from the current source may still be
			// intentionally retained with --exclude. They are valid targets,
			// but are dormant until selected by a later deploy.
			if excluded[strings.ToLower(route.App)] || (app != nil && appMatchesSelectors(excluded, *app)) {
				continue
			}
			return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid,
				"Invalid manifest", fmt.Sprintf("async_routes[%d].app %q does not name a workload in this project source; exclude it explicitly to retain it", i, route.App))
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

func projectWorkloadClassFromScan(workload reposcan.Workload) state.WorkloadClass {
	switch workload.Class {
	case reposcan.ClassGraphQL:
		return state.WorkloadClassGraphQL
	case reposcan.ClassGRPC:
		return state.WorkloadClassGRPC
	case reposcan.ClassJob:
		return state.WorkloadClassJob
	case reposcan.ClassWorker:
		return state.WorkloadClassWorker
	case reposcan.ClassHTTP:
		return state.WorkloadClassHTTP
	default:
		// Match the deploy reconciler: scanner hints that are not in the
		// persisted class enum are conservatively treated as HTTP.
		return state.WorkloadClassHTTP
	}
}

func projectAsyncRoutesPlanProblem(
	plan api.Plan,
	routes []gregalemanifest.AsyncRoute,
	selectedWorkloads []reposcan.Workload,
	projectApps []state.App,
) *api.Problem {
	for _, route := range routes {
		workload, _, found := projectAsyncRouteTarget(route.App, selectedWorkloads, projectApps)
		if !found || workload.Name == "" || !projectWorkloadSelected(workload, selectedWorkloads) {
			continue
		}
		limits, ok := api.LimitsFor(plan)
		if !ok {
			return api.ErrCapacity("could not resolve account plan limits")
		}
		if !limits.AsyncInvokeAllowed {
			return api.ErrPlanEdgeRuleKindNotAllowed(plan, string(state.EdgeRuleKindAsync))
		}
		return nil
	}
	return nil
}

func projectAsyncRouteTarget(slug string, workloads []reposcan.Workload, apps []state.App) (reposcan.Workload, *state.App, bool) {
	for i := range workloads {
		if workloads[i].Name == slug {
			for j := range apps {
				if projectAppMatchesWorkload(apps[j], workloads[i]) {
					return workloads[i], &apps[j], true
				}
			}
			return workloads[i], nil, true
		}
	}
	for i := range apps {
		if apps[i].Slug != slug {
			continue
		}
		for j := range workloads {
			if projectAppMatchesWorkload(apps[i], workloads[j]) {
				return workloads[j], &apps[i], true
			}
		}
		// An existing but currently absent workload is accepted only when it
		// remains in the project inventory (for example, an excluded workload).
		return reposcan.Workload{}, &apps[i], true
	}
	return reposcan.Workload{}, nil, false
}

func projectWorkloadSelected(workload reposcan.Workload, selected []reposcan.Workload) bool {
	for _, candidate := range selected {
		if workload.Name == candidate.Name && workload.RootDir == candidate.RootDir {
			return true
		}
	}
	return false
}

func projectAppMatchesWorkload(app state.App, workload reposcan.Workload) bool {
	name := app.WorkloadName
	if name == "" {
		name = app.Slug
	}
	return name == workload.Name && app.RootDir == workload.RootDir
}

func selectedProjectManifestApps(workloads []reposcan.Workload, apps []state.App) []state.App {
	selected := make([]state.App, 0, len(workloads))
	for _, app := range apps {
		for _, workload := range workloads {
			if projectAppMatchesWorkload(app, workload) {
				selected = append(selected, app)
				break
			}
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Slug != selected[j].Slug {
			return selected[i].Slug < selected[j].Slug
		}
		return selected[i].ID < selected[j].ID
	})
	return selected
}

func (s *server) applyProjectManifestAsyncRoutes(
	ctx context.Context,
	acct state.Account,
	routes []gregalemanifest.AsyncRoute,
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
				s.log.Warn("project async-route rollback incomplete", "app_id", staged[i].appID, "err", err)
			}
		}
	}
	for _, app := range apps {
		appRoutes := projectAsyncRoutesForApp(routes, app)
		change := sourceRefManifestStaged{accountID: acct.ID, appID: app.ID}
		problem := s.applySourceRefManifestAsyncRoutes(ctx, acct, app, appRoutes, &change)
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

func projectAsyncRoutesForApp(routes []gregalemanifest.AsyncRoute, app state.App) []gregalemanifest.AsyncRoute {
	matching := make([]gregalemanifest.AsyncRoute, 0)
	for _, route := range routes {
		if route.App != app.Slug && route.App != app.WorkloadName {
			continue
		}
		route.App = app.Slug
		matching = append(matching, route)
	}
	return matching
}

func projectAsyncRoutePlanWarning(routes []gregalemanifest.AsyncRoute, present, noTriggers bool) string {
	if !present {
		return ""
	}
	if noTriggers {
		return "async_routes skipped by request (--no-triggers); existing async routes left unchanged"
	}
	if len(routes) == 0 {
		return "async_routes: [] clears manifest-owned async routes on selected project workloads"
	}
	return fmt.Sprintf("%d async route declaration(s) will reconcile across selected project workloads; omitted routes on those workloads are cleared", len(routes))
}
