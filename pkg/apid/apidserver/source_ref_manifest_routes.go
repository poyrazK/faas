package apidserver

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// Manifest path routes (ADR-20261021223225) are kind=route edge rules owned
// by their target app, with a kind=rewrite companion for strip_prefix.
const (
	manifestPathRouteKeyPrefix   = "path-route:"
	manifestPathRouteStripSuffix = ":strip"
)

// applySourceRefManifestPathRoutes reconciles the manifest's routes that
// target app. A non-empty list naming other apps leaves this app's routes
// alone; an explicit empty list clears them, like async_routes.
func (s *server) applySourceRefManifestPathRoutes(ctx context.Context, acct state.Account, app state.App, routes []gregalemanifest.PathRoute, staged *sourceRefManifestStaged) *api.Problem {
	manage := routes != nil && len(routes) == 0
	var matching []gregalemanifest.PathRoute
	for _, route := range routes {
		if route.To == app.Slug {
			manage = true
			matching = append(matching, route)
		}
	}
	if !manage {
		return nil
	}
	if len(matching) > 0 && !app.AcceptsRequestInvocations() {
		return api.ErrInvocationWorkloadClass(string(app.WorkloadClass), app.Manifest.ExecutionMode)
	}
	desired := make([]state.CreateEdgeRuleParams, 0, 2*len(matching))
	for _, route := range matching {
		params, problem := sourceRefManifestPathRouteParams(acct, app, route)
		if problem != nil {
			return problem
		}
		desired = append(desired, params...)
	}
	return s.reconcileManifestEdgeRules(ctx, acct, app, manifestEdgeRuleSet{
		prefix: manifestPathRouteKeyPrefix, label: "route",
		kinds:   map[state.EdgeRuleKind]bool{state.EdgeRuleKindRoute: true, state.EdgeRuleKindRewrite: true},
		desired: desired, matches: manifestPathRouteMatches, collides: manifestPathRouteCollides,
	}, staged)
}

// sourceRefManifestPathRouteParams builds the route rule and, for
// strip_prefix, its rewrite rule. Both go through the edge-rules API's own
// body validation so a manifest cannot create a rule the API would refuse.
func sourceRefManifestPathRouteParams(acct state.Account, app state.App, route gregalemanifest.PathRoute) ([]state.CreateEdgeRuleParams, *api.Problem) {
	methods := route.Methods()
	priority := 100
	if route.Priority != nil {
		priority = *route.Priority
	}
	enabled := true
	if route.Enabled != nil {
		enabled = *route.Enabled
	}
	host := strings.ToLower(strings.TrimSpace(route.MatchHost))
	build := func(key string, kind state.EdgeRuleKind, action any, stateAction state.EdgeRuleAction) (state.CreateEdgeRuleParams, *api.Problem) {
		raw, err := json.Marshal(action)
		if err != nil {
			return state.CreateEdgeRuleParams{}, api.ErrCapacity("could not encode route action")
		}
		request := api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: route.MatchPath, MatchMethods: append([]string(nil), methods...),
			Priority: &priority, Enabled: &enabled, Kind: string(kind), Action: raw,
		}
		if problem := edgerules.ValidateEdgeRuleBody(&request, acct.Plan); problem != nil {
			return state.CreateEdgeRuleParams{}, problem
		}
		return state.CreateEdgeRuleParams{
			AccountID: acct.ID, AppID: app.ID, ManifestKey: key,
			MatchHost: request.MatchHost, MatchPath: request.MatchPath,
			MatchMethods: append([]string(nil), methods...), Priority: priority, Enabled: enabled,
			Kind: kind, Action: stateAction,
		}, nil
	}
	key := manifestPathRouteKeyPrefix + route.Name
	routeRule, problem := build(key, state.EdgeRuleKindRoute, api.EdgeRuleRouteAction{TargetAppSlug: app.Slug},
		state.EdgeRuleAction{Kind: state.EdgeRuleKindRoute, Route: &state.EdgeRuleRouteAction{TargetAppSlug: app.Slug}})
	if problem != nil {
		return nil, problem
	}
	out := []state.CreateEdgeRuleParams{routeRule}
	if route.StripPrefix != "" {
		rewriteRule, problem := build(key+manifestPathRouteStripSuffix, state.EdgeRuleKindRewrite, api.EdgeRuleRewriteAction{From: route.StripPrefix, To: "/"},
			state.EdgeRuleAction{Kind: state.EdgeRuleKindRewrite, Rewrite: &state.EdgeRuleRewriteAction{From: route.StripPrefix, To: "/"}})
		if problem != nil {
			return nil, problem
		}
		out = append(out, rewriteRule)
	}
	return out, nil
}

func manifestPathRouteMatches(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool {
	if current.MatchHost != desired.MatchHost || current.MatchPath != desired.MatchPath ||
		!stringSlicesEqual(current.MatchMethods, desired.MatchMethods) || current.Priority != desired.Priority ||
		current.Enabled != desired.Enabled || current.Kind != desired.Kind || len(current.MatchHeaders) != 0 {
		return false
	}
	switch desired.Kind {
	case state.EdgeRuleKindRoute:
		return current.Action.Route != nil && desired.Action.Route != nil && *current.Action.Route == *desired.Action.Route
	case state.EdgeRuleKindRewrite:
		return current.Action.Rewrite != nil && desired.Action.Rewrite != nil && *current.Action.Rewrite == *desired.Action.Rewrite
	}
	return false
}

// manifestPathRouteCollides: an unmanaged rule of the same kind on the same
// host and path would compete with the manifest's rule for the same requests.
func manifestPathRouteCollides(current state.EdgeRule, desired state.CreateEdgeRuleParams) bool {
	return current.Kind == desired.Kind && sameEdgeRuleMatch(current, desired)
}
