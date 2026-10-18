package mcpmanagement

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

func registerContextCatalog(s *mcp.Server, m *management) {
	for _, r := range []*mcp.Resource{
		{URI: "gregale://context", Name: "gregale_connection", Description: "Consented connection metadata; no credentials.", MIMEType: "application/json"},
		{URI: "gregale://default/inventory", Name: "gregale_default_inventory", Description: "Bounded default-scope app inventory; requires default read consent.", MIMEType: "application/json"},
	} {
		s.AddResource(r, m.readContextResource)
	}
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "gregale://projects/{project}/environments/{environment}/inventory", Name: "gregale_environment_inventory", MIMEType: "application/json"}, m.readContextResource)
	s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: regressionResourceTemplate, Name: "gregale_app_regressions", Description: "Bounded app-wide native-visible regression snapshot across environments; separate diagnostic consent required. Disappearance does not prove recovery. Explicit modern subscriptions invalidate this snapshot; reread after changes or reconnecting.", MIMEType: "application/json"}, m.readContextResource)
	for _, view := range []string{"release", "health", "budgets"} {
		s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "gregale://default/apps/{app}/" + view, Name: "gregale_default_" + view, MIMEType: "application/json"}, m.readContextResource)
		s.AddResourceTemplate(&mcp.ResourceTemplate{URITemplate: "gregale://projects/{project}/environments/{environment}/apps/{app}/" + view, Name: "gregale_environment_" + view, MIMEType: "application/json"}, m.readContextResource)
	}
	for _, name := range []string{"gregale_deployment_review", "gregale_rollout_diagnosis", "gregale_rollback_review"} {
		args := []*mcp.PromptArgument{{Name: "app", Required: true, Description: "Existing app slug"}, {Name: "project", Description: "Required with a named environment"}, {Name: "environment", Description: "Registered named environment; omit for default"}}
		if name != "gregale_deployment_review" {
			args = append(args, &mcp.PromptArgument{Name: "deployment_id", Required: true, Description: "Exact observed or retained deployment ID"})
		}
		s.AddPrompt(&mcp.Prompt{Name: name, Description: "Read-only scoped review recipe. Does not plan, apply or approve a change.", Arguments: args}, m.getReviewPrompt)
	}
}

func parseContextURI(uri string) (appArgs, string, error) {
	var args appArgs
	u, err := url.Parse(uri)
	if err != nil || len(uri) > 1024 || u.Scheme != "gregale" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || strings.Contains(uri, "%") {
		return args, "", invalid("invalid_resource", "use a canonical Gregale resource URI without query or encoded segments")
	}
	if uri == "gregale://context" {
		return args, "context", nil
	}
	if uri == "gregale://default/inventory" {
		return args, "inventory", nil
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if u.Host == "apps" && len(parts) == 2 && parts[1] == "regressions" && validateSegment(parts[0], "app") == nil {
		return appArgs{App: parts[0]}, "regressions", nil
	}
	view := ""
	if u.Host == "default" && len(parts) == 3 && parts[0] == "apps" {
		args.App, view = parts[1], parts[2]
	} else if u.Host == "projects" && len(parts) >= 4 && parts[1] == "environments" {
		args.Project, args.Environment = parts[0], parts[2]
		if validateSegment(args.Project, "project") != nil || !api.ValidProjectEnvironmentSlug(args.Environment) {
			return args, "", invalid("invalid_resource", "invalid project/environment resource")
		}
		if len(parts) == 4 && parts[3] == "inventory" {
			return args, "inventory", nil
		}
		if len(parts) == 6 && parts[3] == "apps" {
			args.App, view = parts[4], parts[5]
		}
	}
	if validateSegment(args.App, "app") != nil || view != "release" && view != "health" && view != "budgets" {
		return args, "", invalid("invalid_resource", "unknown Gregale context resource")
	}
	return args, view, nil
}

func (m *management) readContextResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
	defer cancel()
	args, view, err := parseContextURI(req.Params.URI)
	if err != nil {
		return nil, m.contextError(err)
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return nil, m.contextError(err)
	}
	value, err := m.contextResourceValue(ctx, args, view)
	if err != nil {
		return nil, m.contextError(err)
	}
	if err := m.authorizeArgs(ctx, args); err != nil {
		return nil, m.contextError(err)
	}
	value, err = checkedResourceValue(ctx, value)
	if err != nil {
		return nil, m.contextError(err)
	}
	data, err := m.safeOutput(value)
	if err != nil {
		return nil, m.contextError(err)
	}
	result := &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: req.Params.URI, MIMEType: "application/json", Text: string(data.(json.RawMessage))}}}
	encoded, err := json.Marshal(result)
	if err != nil || len(encoded) > maxResultBytes-maxDiagnosticBytes {
		return nil, m.contextError(invalid("response_too_large", "resource exceeds the MCP frame bound; use paginated tools"))
	}
	return result, nil
}
func (m *management) contextResourceValue(ctx context.Context, args appArgs, view string) (any, error) {
	switch view {
	case "context":
		return m.connection(ctx, noArgs{})
	case "regressions":
		return m.regressionResource(ctx, args, "")
	case "inventory":
		if args.Environment != "" {
			return m.getEnvironment(ctx, environmentArgs{Project: args.Project, Environment: args.Environment})
		}
		if err := m.defaultAccess(false); err != nil {
			return nil, err
		}
		return m.listApps(ctx, appsArgs{})
	case "health":
		app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
		if err != nil {
			return nil, err
		}
		h, err := m.scopedHealth(ctx, args)
		if err != nil {
			return nil, err
		}
		if h.AppID != app.ID || h.Scope != normalizedScope(args.Environment) || h.ProjectID != env.ProjectID || h.EnvironmentID != env.ID {
			return nil, invalid("invalid_response", "health resource identity differs from selection")
		}
		return h, nil
	case "budgets":
		return m.getOperationalSummary(ctx, args)
	case "release":
		app, env, err := m.appEnvironment(ctx, args.App, args.Project, args.Environment, false)
		if err != nil {
			return nil, err
		}
		var review api.AppReleaseReview
		if args.Environment == "" {
			review, err = m.client.GetAppReleaseReview(ctx, args.App)
		} else {
			review, err = m.client.GetAppReleaseReviewInEnvironment(ctx, args.App, args.Environment)
		}
		if err != nil {
			return nil, err
		}
		if review.AppID != app.ID || review.Scope != normalizedScope(args.Environment) || review.EnvironmentID != env.ID || review.ProjectID != env.ProjectID {
			return nil, invalid("invalid_response", "release resource identity differs from selection")
		}
		return map[string]any{"app": projectApp(app), "release_review": review}, nil
	}
	return nil, invalid("invalid_resource", "unknown context view")
}
func (m *management) contextError(err error) error {
	data, _ := json.Marshal(m.toolError(err).StructuredContent)
	return &contextRequestError{cause: err, message: fmt.Sprintf("gregale context request failed: %s", data)}
}

type contextRequestError struct {
	cause   error
	message string
}

func (e *contextRequestError) Error() string { return e.message }
func (e *contextRequestError) Unwrap() error { return e.cause }
