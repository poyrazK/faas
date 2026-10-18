package mcpmanagement

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type compareDeploymentsArgs struct {
	App                string `json:"app" jsonschema:"Existing app slug with app-wide diagnostic consent"`
	SourceDeploymentID string `json:"source_deployment_id" jsonschema:"Exact nonzero UUID of a deployment belonging to this app; baseline side"`
	TargetDeploymentID string `json:"target_deployment_id" jsonschema:"Exact different nonzero deployment UUID belonging to this app; comparison side"`
	Route              string `json:"route,omitempty" jsonschema:"Optional exact route path/template without query or fragment; at most 256 bytes"`
	Since              string `json:"since,omitempty" jsonschema:"Positive lookback duration or whole days; at most 64 bytes. Native comparison default is 1h."`
	Until              string `json:"until,omitempty" jsonschema:"Optional RFC3339 window end at or before now; current native plan retention still applies"`
	Limit              int    `json:"limit,omitempty" jsonschema:"Maximum projected routes from 1 to 50; default 20. Output bound only; not a native query limit."`
}

func (m *management) compareSelection(args compareDeploymentsArgs) (api.DebugCompareRequest, int, error) {
	req := api.DebugCompareRequest{Source: args.SourceDeploymentID, Mirror: args.TargetDeploymentID, Route: args.Route, Since: args.Since, Until: args.Until}
	if err := validatePerformanceArgs(performanceArgs{App: args.App, Since: args.Since}); err != nil {
		return req, 0, err
	}
	source, se := uuid.Parse(req.Source)
	target, te := uuid.Parse(req.Mirror)
	if se != nil || te != nil || source == uuid.Nil || target == uuid.Nil || source == target {
		return req, 0, invalid("invalid_argument", "two different nonzero deployment UUIDs are required")
	}
	req.Source, req.Mirror = source.String(), target.String()
	if !requestFilterText(req.Route, api.MCPRequestFieldMaxBytes) || req.Route != "" && (!strings.HasPrefix(req.Route, "/") || strings.ContainsAny(req.Route, "?#")) || m.clean(req.Route) != req.Route {
		return req, 0, invalid("invalid_argument", "route must be an exact path without query, fragment, control characters or credentials")
	}
	if req.Until != "" {
		if !requestFilterText(req.Until, api.MCPCompareUntilMaxBytes) {
			return req, 0, invalid("invalid_argument", "until must be at most 64 bytes without control characters")
		}
		until, err := time.Parse(time.RFC3339Nano, req.Until)
		if err != nil || until.After(time.Now()) {
			return req, 0, invalid("invalid_argument", "until must be an RFC3339 timestamp at or before now")
		}
		req.Until = until.UTC().Format(time.RFC3339Nano)
	}
	limit := args.Limit
	if limit == 0 {
		limit = api.MCPCompareRoutesDefault
	}
	if limit < 1 || limit > api.MCPCompareRoutesMax {
		return req, 0, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	return req, limit, nil
}

func (m *management) compareDeployments(ctx context.Context, args compareDeploymentsArgs) (any, error) {
	req, limit, err := m.compareSelection(args)
	if err != nil {
		return nil, err
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	scopes := map[string]string{}
	for _, id := range []string{req.Source, req.Mirror} {
		if err := m.pinRequestDeployment(ctx, id, app.ID, scopes); err != nil {
			return nil, err
		}
	}
	native, err := m.client.CompareAppDebugDeployments(ctx, args.App, req.Source, req.Mirror, req.Route, req.Since, req.Until)
	if err != nil {
		return nil, err
	}
	view, err := m.projectDeploymentComparison(args.App, app.ID, req, native, scopes, limit)
	if err != nil {
		return nil, err
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error { return m.recheckRequestPage(ctx, args.App, app.ID, scopes) }}, nil
}
