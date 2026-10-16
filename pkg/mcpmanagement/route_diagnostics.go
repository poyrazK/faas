package mcpmanagement

import (
	"context"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func registerRouteDiagnostics(s *mcp.Server, m *management) {
	register(s, m, "gregale_get_route_awake", "Read environment-scoped route wake attribution and estimated running time from retained deployment history. Missing attribution is unknown. Estimates are not billing guarantees.", m.getRouteAwake)
	register(s, m, "gregale_get_route_advice", "Read scoped route advice, deployment-isolated cacheability evidence, and selected-host policy snapshots. Flag duplicate, conflicting, conditional or unavailable policy evidence. No rule bodies or write authority; legacy comparisons are excluded.", m.getRouteAdvice)
}

type routeDiagnosticsArgs struct {
	App         string `json:"app" jsonschema:"Existing app slug"`
	Project     string `json:"project,omitempty" jsonschema:"Project required with a named environment"`
	Environment string `json:"environment,omitempty" jsonschema:"Registered named environment; omit for default"`
	Since       string `json:"since,omitempty" jsonschema:"Positive duration such as 7d or RFC3339 timestamp; native defaults and retention apply"`
	Until       string `json:"until,omitempty" jsonschema:"RFC3339 end timestamp; defaults to now"`
}

type routeAdviceArgs struct {
	routeDiagnosticsArgs
	CacheMaxAgeSeconds int `json:"cache_max_age_seconds,omitempty" jsonschema:"What-if cache lifetime in seconds; omit for native default"`
}

func (m *management) routeDiagnosticsAccess(ctx context.Context, args routeDiagnosticsArgs) error {
	if err := validateSegment(args.App, "app"); err != nil {
		return err
	}
	if len(args.Since) > 64 || len(args.Until) > 64 {
		return invalid("invalid_argument", "diagnostic timestamps are too long")
	}
	if args.Since != "" && !validDiagnosticsSince(args.Since) {
		return invalid("invalid_argument", "since must be a positive duration or RFC3339 timestamp")
	}
	if args.Until != "" {
		if _, err := time.Parse(time.RFC3339Nano, args.Until); err != nil {
			return invalid("invalid_argument", "until must be an RFC3339 timestamp")
		}
	}
	_, _, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	return err
}

func validDiagnosticsSince(value string) bool {
	if _, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return true
	}
	if d, err := time.ParseDuration(value); err == nil {
		return d > 0
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.ParseInt(strings.TrimSuffix(value, "d"), 10, 64)
		return err == nil && days > 0 && days <= math.MaxInt64/int64(24*time.Hour)
	}
	return false
}

func (m *management) getRouteAwake(ctx context.Context, args routeDiagnosticsArgs) (any, error) {
	if err := m.routeDiagnosticsAccess(ctx, args); err != nil {
		return nil, err
	}
	out, err := m.client.GetRouteAwake(ctx, args.App, api.RouteAwakeOptions{Scope: normalizedScope(args.Environment), Since: args.Since, Until: args.Until})
	if err != nil {
		return nil, err
	}
	if out.Slug != args.App || len(out.Routes) > api.RouteAwakeMaxRoutes {
		return nil, invalid("invalid_response", "awake report has an unexpected app or route count")
	}
	if err := m.validateRouteDiagnostics(ctx, args, out.RouteDiagnosticsIdentity); err != nil {
		return nil, err
	}
	return map[string]any{"coverage": out.Coverage, "estimates_only": true, "report": out}, nil
}

func (m *management) getRouteAdvice(ctx context.Context, args routeAdviceArgs) (any, error) {
	if args.CacheMaxAgeSeconds < 0 || args.CacheMaxAgeSeconds > api.ResponseCacheMaxAgeMaxSeconds {
		return nil, invalid("invalid_argument", "cache lifetime exceeds native bounds")
	}
	if err := m.routeDiagnosticsAccess(ctx, args.routeDiagnosticsArgs); err != nil {
		return nil, err
	}
	out, err := m.client.GetRouteAdvice(ctx, args.App, api.RouteAdviceOptions{Scope: normalizedScope(args.Environment), Since: args.Since, Until: args.Until, CacheMaxAgeSeconds: args.CacheMaxAgeSeconds})
	if err != nil {
		return nil, err
	}
	if out.Slug != args.App {
		return nil, invalid("invalid_response", "advice report belongs to another app")
	}
	if err := m.validateRouteDiagnostics(ctx, args.routeDiagnosticsArgs, out.RouteDiagnosticsIdentity); err != nil {
		return nil, err
	}
	if out.CacheabilityCoverage != "deployment_isolated_observations_only" {
		return nil, invalid("invalid_response", "advice response lacks isolated cacheability provenance")
	}
	if err := prepareRouteAdvicePolicy(&out); err != nil {
		return nil, err
	}
	return map[string]any{"coverage": out.Coverage, "estimates_only": true, "report": out}, nil
}

func (m *management) validateRouteDiagnostics(ctx context.Context, args routeDiagnosticsArgs, identity api.RouteDiagnosticsIdentity) error {
	app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
	if err != nil {
		return err
	}
	if identity.AppID != app.ID || identity.Scope != normalizedScope(args.Environment) || identity.EnvironmentID != env.ID || identity.ProjectID != env.ProjectID || identity.Coverage != "retained_deployment_history" || !identity.Partial {
		return invalid("invalid_response", "diagnostic report lacks exact environment attribution and coverage")
	}
	return nil
}
