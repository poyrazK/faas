package mcpmanagement

import (
	"context"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

type requestsArgs struct {
	App          string `json:"app" jsonschema:"Existing app slug with app-wide request diagnostic consent"`
	Since        string `json:"since,omitempty" jsonschema:"Positive duration or whole days, e.g. 1h or 3d; native default 24h and plan retention clamp apply. Repeat unchanged with a cursor."`
	Route        string `json:"route,omitempty" jsonschema:"Exact route path or template without query or fragment; at most 256 bytes"`
	DeploymentID string `json:"deployment_id,omitempty" jsonschema:"Optional deployment UUID belonging to this app; filters diagnostics across its environments"`
	Status       int    `json:"status,omitempty" jsonschema:"Optional exact HTTP status from 100 to 599"`
	MinLatencyMS int    `json:"min_latency_ms,omitempty" jsonschema:"Minimum retained latency in milliseconds, from 0 to 86400000"`
	ColdBoot     *bool  `json:"cold_boot,omitempty" jsonschema:"Filter cold or warm requests; false differs from omission"`
	Limit        int    `json:"limit,omitempty" jsonschema:"Rows per page from 1 to 50; default 20"`
	Cursor       string `json:"cursor,omitempty" jsonschema:"Opaque next_cursor from a previous page; repeat all original filters unchanged"`
}

func requestListOptions(args requestsArgs) (api.DebugTelemetryListOptions, error) {
	opts := api.DebugTelemetryListOptions{Since: args.Since, Route: args.Route, DeploymentID: args.DeploymentID, Status: args.Status, MinLatencyMS: args.MinLatencyMS, ColdBoot: args.ColdBoot, Limit: args.Limit, Cursor: args.Cursor}
	if opts.Limit == 0 {
		opts.Limit = api.MCPRequestListDefault
	}
	if opts.Limit < 1 || opts.Limit > api.MCPRequestListMax {
		return opts, invalid("invalid_argument", "limit must be between 1 and 50")
	}
	if args.Status != 0 && (args.Status < 100 || args.Status > 599) || args.MinLatencyMS < 0 || args.MinLatencyMS > api.MCPRequestMinLatencyMaxMS {
		return opts, invalid("invalid_argument", "status must be 100..599 and min_latency_ms must be 0..86400000")
	}
	if args.Since != "" {
		if _, err := requestSince(args.Since); err != nil {
			return opts, err
		}
	}
	if !requestFilterText(args.Route, api.MCPRequestFieldMaxBytes) || args.Route != "" && (!strings.HasPrefix(args.Route, "/") || strings.ContainsAny(args.Route, "?#")) {
		return opts, invalid("invalid_argument", "route must be an exact path without query, fragment or control characters, at most 256 bytes")
	}
	if !requestCursor(args.Cursor) {
		return opts, invalid("invalid_argument", "cursor must be copied unchanged from next_cursor and be at most 8192 bytes")
	}
	if args.DeploymentID != "" {
		id, err := uuid.Parse(args.DeploymentID)
		if err != nil || id == uuid.Nil {
			return opts, invalid("invalid_argument", "deployment_id must be a nonzero deployment UUID")
		}
		opts.DeploymentID = id.String()
	}
	return opts, nil
}

func requestFilterText(value string, maxBytes int) bool {
	return len(value) <= maxBytes && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func requestCursor(value string) bool {
	return requestFilterText(value, api.MCPRequestCursorMaxBytes) && !strings.ContainsFunc(value, unicode.IsSpace)
}

func (m *management) listRequests(ctx context.Context, args requestsArgs) (any, error) {
	opts, err := requestListOptions(args)
	if err != nil {
		return nil, err
	}
	if m.clean(opts.Route) != opts.Route {
		return nil, invalid("invalid_argument", "route filters must not contain credentials")
	}
	app, err := m.requestApp(ctx, args.App)
	if err != nil {
		return nil, err
	}
	scopes := map[string]string{}
	if err := m.pinRequestDeployment(ctx, opts.DeploymentID, app.ID, scopes); err != nil {
		return nil, err
	}
	page, err := m.client.ListAppDebugRequestsWithOptions(ctx, args.App, opts)
	if err != nil {
		return nil, err
	}
	if err := m.validateRequestPage(page, opts); err != nil {
		return nil, err
	}
	view, err := m.projectRequestPage(ctx, args.App, app.ID, page, opts, scopes)
	if err != nil {
		return nil, err
	}
	return &guardedRead{value: view, check: func(ctx context.Context) error {
		return m.recheckRequestPage(ctx, args.App, app.ID, scopes)
	}}, nil
}

func (m *management) pinRequestDeployment(ctx context.Context, id, appID string, scopes map[string]string) error {
	if id == "" {
		return nil
	}
	if _, ok := scopes[id]; ok {
		return nil
	}
	scope, err := m.requestDeployment(ctx, id, appID)
	if err == nil {
		scopes[id] = scope
	}
	return err
}

func (m *management) recheckRequestPage(ctx context.Context, slug, appID string, scopes map[string]string) error {
	for id, scope := range scopes {
		current, err := m.requestDeployment(ctx, id, appID)
		if err != nil {
			return err
		}
		if current != scope {
			return invalid("invalid_response", "request deployment scope changed during discovery")
		}
	}
	app, err := m.requestApp(ctx, slug)
	if err == nil && app.ID != appID {
		return invalid("request_access_denied", "app identity changed during request discovery")
	}
	return err
}

func (m *management) validateRequestPage(page api.DebugTelemetryListResponse, opts api.DebugTelemetryListOptions) error {
	if len(page.Requests) > opts.Limit || page.Complete != (page.NextCursor == "") || !page.Complete && (len(page.Requests) == 0 || page.NextCursor == opts.Cursor) {
		return invalid("invalid_response", "request page exceeds its bound or has inconsistent pagination")
	}
	if !requestCursor(page.NextCursor) || m.clean(page.NextCursor) != page.NextCursor {
		return invalid("invalid_response", "native request cursor is unsafe to return unchanged")
	}
	f := page.Filters
	if f.DeploymentID != opts.DeploymentID || f.Status != opts.Status || f.MinLatencyMS != opts.MinLatencyMS || f.ConsumerID != "" || !sameRequestColdBoot(f.ColdBoot, opts.ColdBoot) {
		return invalid("invalid_response", "native request page filters differ from the selection")
	}
	start, startErr := time.Parse(time.RFC3339Nano, page.WindowStart)
	end, endErr := time.Parse(time.RFC3339Nano, page.WindowEnd)
	window, sinceErr := requestSince(page.Since)
	if startErr != nil || endErr != nil || sinceErr != nil || !start.Before(end) || end.Sub(start) != window {
		return invalid("invalid_response", "native request page has inconsistent retained window metadata")
	}
	if opts.Since != "" {
		requested, _ := requestSince(opts.Since)
		if window > requested || window != requested && !page.RetentionClamped {
			return invalid("invalid_response", "native request window changed without retention clamping")
		}
	}
	return nil
}

func sameRequestColdBoot(a, b *bool) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
