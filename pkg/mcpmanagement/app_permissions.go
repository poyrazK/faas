package mcpmanagement

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *management) appIDAllowed(id string) bool {
	if m.opts.AllowedApps == nil {
		return true
	}
	for _, allowed := range m.opts.AllowedApps {
		if allowed == id {
			return true
		}
	}
	return false
}

// Every tool goes through this guard. App identities prevent deleted/recreated
// slugs from inheriting a grant. ID-only calls cannot bypass app consent.
func (m *management) authorizeArgs(ctx context.Context, args any) error {
	return m.authorizeToolArgs(ctx, args, false)
}

func (m *management) authorizeToolArgs(ctx context.Context, args any, write bool) error {
	if diagnostic, ok := args.(profilesArgs); ok {
		if write {
			return invalid("request_access_denied", "profile diagnostics are read-only")
		}
		// Profile selection is app-wide diagnostics. The handler pins exact
		// deployment ownership/scope without granting environment inspection.
		args = appArgs{App: diagnostic.App}
	}
	if diagnostic, ok := args.(compareDeploymentsArgs); ok {
		if write {
			return invalid("request_access_denied", "deployment comparison is read-only")
		}
		// Exact deployment ownership/scope is guarded under app-wide diagnostic
		// consent by the comparison handler, independently of environment reads.
		args = appArgs{App: diagnostic.App}
	}
	if diagnostic, ok := args.(requestsArgs); ok {
		if write {
			return invalid("request_access_denied", "request discovery is read-only")
		}
		// This deployment ID is a filter under app-wide diagnostic consent.
		// The discovery handler pins its app/scope, including named scopes;
		// ordinary deployment tools retain their environment authorization.
		args = appArgs{App: diagnostic.App}
	}
	if m.opts.AllowedApps == nil && m.opts.Environments == nil {
		return nil
	}
	data, err := json.Marshal(args)
	if err != nil {
		return err
	}
	var target struct {
		App         string `json:"app"`
		Deployment  string `json:"deployment_id"`
		Target      string `json:"target_deployment_id"`
		Project     string `json:"project"`
		Environment string `json:"environment"`
		From        string `json:"from_environment"`
		To          string `json:"to_environment"`
		Promotion   string `json:"promotion_id"`
	}
	if err := json.Unmarshal(data, &target); err != nil {
		return err
	}
	if target.App != "" && target.Environment == "" {
		if err := m.defaultAccess(write); err != nil {
			return err
		}
	}
	for _, environment := range []string{target.Environment, target.From, target.To} {
		if environment == "" {
			continue
		}
		wholeProject := target.From != "" || target.To != "" || target.Promotion != ""
		if _, _, err := m.environmentAccess(ctx, target.Project, environment, write && environment != target.From, wholeProject); err != nil {
			return err
		}
	}
	if target.App != "" && target.Environment != "" {
		if _, _, err := m.appEnvironment(ctx, target.App, target.Project, target.Environment, write); err != nil {
			return err
		}
	}
	if target.App != "" && m.opts.AllowedApps != nil {
		id, ok := m.opts.AllowedApps[target.App]
		if !ok {
			return invalid("app_access_denied", "app is outside this connection's consent")
		}
		app, err := m.client.GetApp(ctx, target.App)
		if err != nil {
			return err
		}
		if app.ID != id {
			return invalid("app_access_denied", "app identity changed; reconnect to review access")
		}
	}
	for _, id := range []string{target.Deployment, target.Target} {
		if id == "" {
			continue
		}
		if err := validateSegment(id, "deployment_id"); err != nil {
			return err
		}
		d, err := m.client.GetDeployment(ctx, id)
		if err != nil {
			return err
		}
		if err := m.deploymentAccess(ctx, d); err != nil {
			return err
		}
	}
	return nil
}

func (m *management) filterApps(apps []api.AppResponse) []api.AppResponse {
	if m.opts.AllowedApps == nil {
		return apps
	}
	out := make([]api.AppResponse, 0, len(apps))
	for _, app := range apps {
		if m.opts.AllowedApps[app.Slug] == app.ID {
			out = append(out, app)
		}
	}
	return out
}
