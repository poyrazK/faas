package mcpmanagement

import (
	"context"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Completion is a bounded read, never an authorization decision or write plan.
func (m *management) completeArgument(ctx context.Context, req *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
	ctx, cancel := context.WithTimeout(ctx, m.opts.Timeout)
	defer cancel()
	if req.Params.Ref != nil && req.Params.Ref.Type == "ref/prompt" && (req.Params.Ref.Name == "gregale_performance_diagnosis" || req.Params.Ref.Name == "gregale_residency_diagnosis") {
		result, err := m.completePerformanceArgument(ctx, req)
		if err != nil {
			return nil, m.contextError(err)
		}
		return result, nil
	}
	if req.Params.Ref != nil && req.Params.Ref.Type == "ref/prompt" && req.Params.Ref.Name == "gregale_request_diagnosis" {
		result, err := m.completeRequestArgument(ctx, req)
		if err != nil {
			return nil, m.contextError(err)
		}
		return result, nil
	}
	fields, err := completionFields(req.Params.Ref)
	if err != nil {
		return nil, m.contextError(err)
	}
	p := req.Params
	if !fields[p.Argument.Name] || !completionPrefix(p.Argument.Value) {
		return nil, m.contextError(invalid("invalid_argument", "invalid completion argument or prefix"))
	}
	args := map[string]string{}
	if p.Context != nil {
		for key, value := range p.Context.Arguments {
			if !fields[key] || len(value) > 200 || (value != "" && validateSegment(value, key) != nil) {
				return nil, m.contextError(invalid("invalid_argument", "invalid completion context"))
			}
			if key != p.Argument.Name {
				args[key] = value
			}
		}
	}
	if p.Ref.Type == "ref/resource" && strings.HasPrefix(p.Ref.URI, "gregale://projects/") && p.Argument.Name == "app" && (args["project"] == "" || args["environment"] == "") {
		return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}}}, nil
	}
	if p.Ref.Type == "ref/resource" && p.Ref.URI == regressionResourceTemplate {
		result, err := m.completeRequestApps(ctx, p.Argument.Value)
		if err != nil {
			return nil, m.contextError(err)
		}
		return result, nil
	}
	values, more, err := m.completionCandidates(ctx, p.Argument.Name, args)
	if err != nil {
		return nil, m.contextError(err)
	}
	result, err := completionResult(values, p.Argument.Value, more)
	if err != nil {
		return nil, m.contextError(err)
	}
	return result, nil
}

func completionResult(values []string, prefix string, more bool) (*mcp.CompleteResult, error) {
	unique := map[string]bool{}
	for _, value := range values {
		if validateSegment(value, "candidate") != nil {
			return nil, invalid("invalid_response", "invalid completion candidate")
		}
		if strings.HasPrefix(value, prefix) {
			unique[value] = true
		}
	}
	values = []string{}
	for value := range unique {
		values = append(values, value)
	}
	sort.Strings(values)
	total := len(values)
	more = more || total > maxPageSize
	values = values[:min(total, maxPageSize)]
	result := &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: values, HasMore: more}}
	if !more {
		result.Completion.Total = total
	}
	return result, nil
}

func completionPrefix(value string) bool {
	if len(value) > 200 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func completionFields(ref *mcp.CompleteReference) (map[string]bool, error) {
	fields := map[string]bool{}
	if ref != nil && ref.Type == "ref/prompt" {
		switch ref.Name {
		case "gregale_deployment_review":
		case "gregale_rollout_diagnosis", "gregale_rollback_review":
			fields["deployment_id"] = true
		default:
			return nil, invalid("invalid_argument", "unknown completion reference")
		}
		fields["app"], fields["project"], fields["environment"] = true, true, true
		return fields, nil
	}
	if ref != nil && ref.Type == "ref/resource" {
		if ref.URI == regressionResourceTemplate {
			return map[string]bool{"app": true}, nil
		}
		for _, view := range []string{"release", "health", "budgets"} {
			if ref.URI == "gregale://default/apps/{app}/"+view {
				return map[string]bool{"app": true}, nil
			}
			if ref.URI == "gregale://projects/{project}/environments/{environment}/apps/{app}/"+view {
				return map[string]bool{"app": true, "project": true, "environment": true}, nil
			}
		}
		if ref.URI == "gregale://projects/{project}/environments/{environment}/inventory" {
			return map[string]bool{"project": true, "environment": true}, nil
		}
	}
	return nil, invalid("invalid_argument", "unknown completion reference")
}

func (m *management) completionCandidates(ctx context.Context, field string, args map[string]string) ([]string, bool, error) {
	switch field {
	case "project", "environment":
		return m.completeEnvironment(ctx, field, args)
	case "app":
		return m.completeApps(ctx, args)
	case "deployment_id":
		return m.completeDeployments(ctx, args)
	}
	return nil, false, invalid("invalid_argument", "unsupported completion argument")
}

func (m *management) completeEnvironment(ctx context.Context, field string, args map[string]string) ([]string, bool, error) {
	if field == "environment" && args["project"] == "" {
		return []string{}, false, nil
	}
	grants := []EnvironmentGrant{}
	if m.opts.Environments != nil {
		for _, g := range m.opts.Environments.Named {
			grants = append(grants, g)
		}
	} else {
		var err error
		grants, err = discoverEnvironments(ctx, m.client)
		if err != nil {
			return nil, false, err
		}
	}
	if len(grants) > maxOAuthEntries {
		return nil, false, invalid("response_too_large", "too many environments for completion")
	}
	values := []string{}
	for _, g := range grants {
		if field == "environment" && g.Project != args["project"] || args["environment"] != "" && g.Environment != args["environment"] {
			continue
		}
		if m.opts.AllowedApps != nil {
			eligible := false
			for slug, id := range g.Apps {
				eligible = eligible || m.opts.AllowedApps[slug] == id
			}
			if !eligible {
				continue
			}
		}
		if args["app"] != "" && g.Apps[args["app"]] == "" {
			continue
		}
		p, e, err := m.environmentAccess(ctx, g.Project, g.Environment, false, false)
		if err != nil {
			return nil, false, err
		}
		if p.ID != g.ProjectID || e.ID != g.EnvironmentID {
			return nil, false, invalid("identity_changed", "completion environment identity changed")
		}
		if args["app"] != "" {
			if _, _, err := m.appEnvironment(ctx, args["app"], g.Project, g.Environment, false); err != nil {
				return nil, false, err
			}
		}
		if field == "project" {
			values = append(values, g.Project)
		} else {
			values = append(values, g.Environment)
		}
	}
	return values, false, nil
}

func (m *management) completeApps(ctx context.Context, args map[string]string) ([]string, bool, error) {
	values := []string{}
	if args["project"] != "" && args["environment"] == "" || args["environment"] != "" && args["project"] == "" {
		return values, false, nil
	}
	if args["environment"] != "" {
		p, env, err := m.environmentAccess(ctx, args["project"], args["environment"], false, false)
		if err != nil {
			return nil, false, err
		}
		if len(p.Workloads) > maxOAuthEntries {
			return nil, false, invalid("response_too_large", "too many workloads for completion")
		}
		for _, w := range p.Workloads {
			if m.opts.AllowedApps != nil && m.opts.AllowedApps[w.Slug] == "" {
				continue
			}
			if m.opts.Environments != nil && m.opts.Environments.Named[args["project"]+"/"+args["environment"]].Apps[w.Slug] == "" {
				continue
			}
			if _, _, err := m.appEnvironment(ctx, w.Slug, args["project"], args["environment"], false); err != nil {
				return nil, false, err
			}
			values = append(values, w.Slug)
		}
		current, currentEnv, err := m.environmentAccess(ctx, args["project"], args["environment"], false, false)
		if err != nil {
			return nil, false, err
		}
		if current.ID != p.ID || currentEnv.ID != env.ID {
			return nil, false, invalid("identity_changed", "completion environment identity changed")
		}
	} else {
		apps, err := m.client.ListApps(ctx)
		if err != nil {
			return nil, false, err
		}
		if len(apps) > maxOAuthEntries {
			return nil, false, invalid("response_too_large", "too many apps for completion")
		}
		for _, a := range m.filterApps(apps) {
			if a.ID == "" {
				return nil, false, invalid("invalid_response", "missing app identity")
			}
			values = append(values, a.Slug)
		}
	}
	return values, false, nil
}

func (m *management) completeDeployments(ctx context.Context, args map[string]string) ([]string, bool, error) {
	if args["app"] == "" || (args["project"] == "") != (args["environment"] == "") {
		return []string{}, false, nil
	}
	target := appArgs{App: args["app"], Project: args["project"], Environment: args["environment"]}
	if err := m.authorizeArgs(ctx, target); err != nil {
		return nil, false, err
	}
	app, env, err := m.appEnvironment(ctx, target.App, target.Project, target.Environment, false)
	if err != nil {
		return nil, false, err
	}
	if app.ID == "" || app.Slug != target.App || !m.appIDAllowed(app.ID) {
		return nil, false, invalid("invalid_response", "completion app identity differs from selection")
	}
	page, err := m.client.ListAppDeployments(ctx, target.App, "", maxPageSize)
	if err != nil {
		return nil, false, err
	}
	if len(page.Items) > maxPageSize {
		return nil, false, invalid("invalid_response", "deployment page exceeds requested bound")
	}
	values := []string{}
	for _, d := range page.Items {
		if d.AppID != app.ID {
			return nil, false, invalid("invalid_response", "deployment belongs to another app")
		}
		if normalizedScope(d.Scope) == normalizedScope(target.Environment) {
			values = append(values, d.ID)
		}
	}
	current, currentEnv, err := m.appEnvironment(ctx, target.App, target.Project, target.Environment, false)
	if err != nil {
		return nil, false, err
	}
	if current.ID != app.ID || currentEnv.ID != env.ID || currentEnv.ProjectID != env.ProjectID {
		return nil, false, invalid("identity_changed", "completion target identity changed")
	}
	return values, page.NextBefore != "", nil
}
