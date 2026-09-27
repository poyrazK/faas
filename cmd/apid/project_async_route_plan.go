package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectAsyncRoutePlanTarget struct {
	workload reposcan.Workload
	app      state.App
	selected bool
	routes   []gregalemanifest.AsyncRoute
	rules    []state.EdgeRule
}

// planProjectManifestAsyncRoutes computes the pre-apply async-route diff.
// It mirrors applyProjectManifestAsyncRoutes' selected-app scope: a declared
// empty list clears managed routes only on selected workloads, while omitted
// workloads and --no-triggers are explicitly represented as skipped.
func (s *server) planProjectManifestAsyncRoutes(
	ctx context.Context,
	acct state.Account,
	allWorkloads, selectedWorkloads []reposcan.Workload,
	projectApps []state.App,
	routes []gregalemanifest.AsyncRoute,
	noTriggers bool,
) ([]api.PlanAsyncRoute, *api.Problem) {
	targets := make([]*projectAsyncRoutePlanTarget, 0, len(allWorkloads)+len(routes))
	byWorkload := make(map[string]*projectAsyncRoutePlanTarget, len(allWorkloads))
	byAppID := make(map[string]*projectAsyncRoutePlanTarget, len(projectApps))
	byAppSlug := make(map[string]*projectAsyncRoutePlanTarget, len(projectApps))
	addTarget := func(workload reposcan.Workload, app state.App) *projectAsyncRoutePlanTarget {
		key := projectAsyncRouteWorkloadKey(workload)
		if workload.Name != "" {
			if target := byWorkload[key]; target != nil {
				return target
			}
		}
		selected := workload.Name != "" && projectWorkloadSelected(workload, selectedWorkloads)
		target := &projectAsyncRoutePlanTarget{workload: workload, app: app, selected: selected}
		targets = append(targets, target)
		if workload.Name != "" {
			byWorkload[key] = target
		}
		if app.ID != "" {
			byAppID[app.ID] = target
		}
		if app.Slug != "" {
			byAppSlug[app.Slug] = target
		}
		return target
	}

	for _, workload := range allWorkloads {
		var app state.App
		for _, candidate := range projectApps {
			if projectAppMatchesWorkload(candidate, workload) {
				app = candidate
				break
			}
		}
		if app.ID == "" {
			app = state.App{
				AccountID:     acct.ID,
				Slug:          workload.Name,
				WorkloadName:  workload.Name,
				RootDir:       workload.RootDir,
				WorkloadClass: projectWorkloadClassFromScan(workload),
			}
		}
		addTarget(workload, app)
	}

	var unknownTargetRows []api.PlanAsyncRoute
	for _, route := range routes {
		workload, app, found := projectAsyncRouteTarget(route.App, allWorkloads, projectApps)
		if !found {
			if noTriggers {
				unknownTargetRows = append(unknownTargetRows, planAsyncRouteFromManifest(route, "skipped", "--no-triggers is set; async routes remain unchanged"))
			}
			continue
		}
		var target *projectAsyncRoutePlanTarget
		if workload.Name != "" {
			target = byWorkload[projectAsyncRouteWorkloadKey(workload)]
		} else if app != nil {
			target = byAppID[app.ID]
			if target == nil {
				target = byAppSlug[app.Slug]
			}
			if target == nil {
				target = addTarget(workload, *app)
			}
		}
		if target == nil {
			continue
		}
		target.routes = append(target.routes, route)
	}

	for _, target := range targets {
		if target.app.ID == "" {
			continue
		}
		rules, err := s.store.ListEdgeRulesForApp(ctx, target.app.ID)
		if err != nil {
			return nil, api.ErrCapacity("could not list app edge rules for async route plan")
		}
		target.rules = rules
	}

	var planned []api.PlanAsyncRoute
	planned = append(planned, unknownTargetRows...)
	for _, target := range targets {
		selected := target.selected && !noTriggers
		validateSelectedState := selected
		reason := ""
		if noTriggers {
			reason = "--no-triggers is set; async routes remain unchanged"
		} else if !target.selected {
			if target.workload.Name == "" {
				reason = "workload is not present in the selected project scan"
			} else {
				reason = "workload is not selected by this deploy"
			}
		}

		managedByKey := make(map[string]state.EdgeRule)
		managedCount := 0
		for _, rule := range target.rules {
			if !strings.HasPrefix(rule.ManifestKey, manifestAsyncRouteKeyPrefix) {
				continue
			}
			if validateSelectedState && rule.Kind != state.EdgeRuleKindAsync {
				return nil, api.ErrCapacity("manifest-owned async route has an unexpected edge-rule kind")
			}
			if _, duplicate := managedByKey[rule.ManifestKey]; duplicate && validateSelectedState {
				return nil, api.ErrCapacity("duplicate manifest-owned async route identity")
			}
			managedByKey[rule.ManifestKey] = rule
			managedCount++
		}

		if !selected {
			wanted := make(map[string]struct{}, len(target.routes))
			for _, route := range target.routes {
				key := manifestAsyncRouteKeyPrefix + route.Name
				wanted[key] = struct{}{}
				planned = append(planned, planAsyncRouteFromManifest(route, "skipped", reason))
			}
			for key, rule := range managedByKey {
				if _, hasDesired := wanted[key]; hasDesired {
					continue
				}
				planned = append(planned, planAsyncRouteFromRule(target.app.Slug, rule, "skipped", reason))
			}
			continue
		}

		desiredByKey := make(map[string]state.CreateEdgeRuleParams, len(target.routes))
		for _, route := range target.routes {
			route.App = target.app.Slug
			params, problem := s.sourceRefManifestAsyncRouteParams(ctx, acct, target.app, route)
			if problem != nil {
				return nil, problem
			}
			if _, duplicate := desiredByKey[params.ManifestKey]; duplicate {
				return nil, api.ErrEdgeRuleConflict(fmt.Sprintf("duplicate async route identity %q for app %s", route.Name, target.app.Slug))
			}
			desiredByKey[params.ManifestKey] = params
		}

		for key, desired := range desiredByKey {
			for _, current := range target.rules {
				if current.ManifestKey == "" && sameEdgeRuleMatch(current, desired) {
					return nil, api.ErrEdgeRuleConflict(fmt.Sprintf("async route %q overlaps unmanaged edge rule %s; remove or change the existing rule first", strings.TrimPrefix(key, manifestAsyncRouteKeyPrefix), current.ID))
				}
			}
		}
		limits, ok := api.LimitsFor(acct.Plan)
		if !ok {
			return nil, api.ErrCapacity("could not resolve account plan limits")
		}
		finalRuleCount := len(target.rules) - managedCount + len(desiredByKey)
		if finalRuleCount > limits.EdgeRulesPerApp {
			return nil, api.ErrPlanLimitEdgeRules(acct.Plan, limits.EdgeRulesPerApp, finalRuleCount)
		}

		keys := make(map[string]struct{}, len(managedByKey)+len(desiredByKey))
		for key := range managedByKey {
			keys[key] = struct{}{}
		}
		for key := range desiredByKey {
			keys[key] = struct{}{}
		}
		orderedKeys := make([]string, 0, len(keys))
		for key := range keys {
			orderedKeys = append(orderedKeys, key)
		}
		sort.Strings(orderedKeys)
		for _, key := range orderedKeys {
			current, exists := managedByKey[key]
			desired, wanted := desiredByKey[key]
			switch {
			case wanted && !exists:
				planned = append(planned, planAsyncRouteFromParams(target.app.Slug, desired, "create", ""))
			case wanted && exists:
				action := "unchanged"
				if !sourceRefManifestEdgeRuleMatches(current, desired) {
					action = "update"
				}
				planned = append(planned, planAsyncRouteFromParams(target.app.Slug, desired, action, ""))
			case exists:
				planned = append(planned, planAsyncRouteFromRule(target.app.Slug, current, "remove", "route is no longer declared by the project manifest"))
			}
		}
	}

	sort.Slice(planned, func(i, j int) bool {
		if planned[i].App != planned[j].App {
			return planned[i].App < planned[j].App
		}
		if planned[i].Name != planned[j].Name {
			return planned[i].Name < planned[j].Name
		}
		return planned[i].Action < planned[j].Action
	})
	return planned, nil
}

func projectAsyncRouteWorkloadKey(workload reposcan.Workload) string {
	return workload.RootDir + "\x00" + workload.Name
}

func planAsyncRouteFromManifest(route gregalemanifest.AsyncRoute, action, reason string) api.PlanAsyncRoute {
	methods := append([]string(nil), route.MatchMethods...)
	if len(methods) == 0 {
		methods = []string{"POST"}
	}
	for i := range methods {
		methods[i] = strings.ToUpper(strings.TrimSpace(methods[i]))
	}
	sort.Strings(methods)
	priority := 100
	if route.Priority != nil {
		priority = *route.Priority
	}
	enabled := true
	if route.Enabled != nil {
		enabled = *route.Enabled
	}
	return api.PlanAsyncRoute{
		App: route.App, Name: route.Name, Action: action,
		MatchHost: strings.ToLower(strings.TrimSpace(route.MatchHost)), MatchPath: route.MatchPath,
		MatchMethods: methods, Priority: priority, Enabled: enabled,
		OnSuccess: route.OnSuccess, OnFailure: route.OnFailure,
		RetryPolicy: retryPolicyDTOFromManifest(route.RetryPolicy), MaxAgeSeconds: route.MaxAgeSeconds,
		Reason: reason,
	}
}

func planAsyncRouteFromParams(app string, params state.CreateEdgeRuleParams, action, reason string) api.PlanAsyncRoute {
	row := api.PlanAsyncRoute{
		App: app, Name: strings.TrimPrefix(params.ManifestKey, manifestAsyncRouteKeyPrefix), Action: action,
		MatchHost: params.MatchHost, MatchPath: params.MatchPath,
		MatchMethods: append([]string{}, params.MatchMethods...), Priority: params.Priority, Enabled: params.Enabled,
		Reason: reason,
	}
	if params.Action.Async != nil {
		row.OnSuccess = params.Action.Async.OnSuccess
		row.OnFailure = params.Action.Async.OnFailure
		row.RetryPolicy = clonePlanRetryPolicy(params.Action.Async.RetryPolicy)
		row.MaxAgeSeconds = params.Action.Async.MaxAgeSeconds
	}
	return row
}

func planAsyncRouteFromRule(app string, rule state.EdgeRule, action, reason string) api.PlanAsyncRoute {
	row := api.PlanAsyncRoute{
		App: app, Name: strings.TrimPrefix(rule.ManifestKey, manifestAsyncRouteKeyPrefix), Action: action,
		MatchHost: rule.MatchHost, MatchPath: rule.MatchPath,
		MatchMethods: append([]string{}, rule.MatchMethods...), Priority: rule.Priority, Enabled: rule.Enabled,
		Reason: reason,
	}
	if rule.Action.Async != nil {
		row.OnSuccess = rule.Action.Async.OnSuccess
		row.OnFailure = rule.Action.Async.OnFailure
		row.RetryPolicy = clonePlanRetryPolicy(rule.Action.Async.RetryPolicy)
		row.MaxAgeSeconds = rule.Action.Async.MaxAgeSeconds
	}
	return row
}

func clonePlanRetryPolicy(policy *api.RetryPolicyDTO) *api.RetryPolicyDTO {
	if policy == nil {
		return nil
	}
	copy := *policy
	return &copy
}
