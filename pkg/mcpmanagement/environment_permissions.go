package mcpmanagement

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
)

// EnvironmentConsent keeps default writes independent of named environment
// writes. A named grant pins registry and app identities, never just a slug.
type EnvironmentConsent struct {
	DefaultWrite bool                        `json:"default_write"`
	Named        map[string]EnvironmentGrant `json:"named"`
	// RequestReadApps grants app-wide retained diagnostics independently of
	// default/named environment access. Missing entries deny remote access.
	RequestReadApps     map[string]string `json:"request_read_apps,omitempty"`
	RegressionWriteApps map[string]string `json:"regression_write_apps,omitempty"`
}

type EnvironmentGrant struct {
	Project       string            `json:"project"`
	ProjectID     string            `json:"project_id"`
	Environment   string            `json:"environment"`
	EnvironmentID string            `json:"environment_id"`
	Apps          map[string]string `json:"apps"`
	Write         bool              `json:"write"`
}

func (c *EnvironmentConsent) validate() error {
	if len(c.RegressionWriteApps) > maxOAuthEntries {
		return errors.New("too many regression triage grants")
	}
	for slug, id := range c.RegressionWriteApps {
		if id == "" || c.RequestReadApps[slug] != id {
			return errors.New("regression triage requires the same diagnostic app identity")
		}
	}
	if len(c.RequestReadApps) > maxOAuthEntries {
		return errors.New("too many request diagnostic app grants")
	}
	for slug, id := range c.RequestReadApps {
		if validateSegment(slug, "app") != nil || validateSegment(id, "app_id") != nil {
			return errors.New("invalid request diagnostic app identity")
		}
	}
	if len(c.Named) > maxOAuthEntries {
		return errors.New("too many consented environments")
	}
	for key, g := range c.Named {
		if key != g.Project+"/"+g.Environment || validateSegment(g.Project, "project") != nil || !api.ValidProjectEnvironmentSlug(g.Environment) || validateSegment(g.ProjectID, "project_id") != nil || validateSegment(g.EnvironmentID, "environment_id") != nil {
			return errors.New("invalid consented environment identity")
		}
		for slug, id := range g.Apps {
			if validateSegment(slug, "app") != nil || validateSegment(id, "app_id") != nil {
				return errors.New("invalid environment app identity")
			}
		}
	}
	return nil
}

func (m *management) defaultAccess(write bool) error {
	if write && m.opts.Environments != nil && !m.opts.Environments.DefaultWrite {
		return invalid("environment_access_denied", "default environment writes were not consented")
	}
	return nil
}

func (m *management) environmentAccess(ctx context.Context, project, environment string, write, wholeProject bool) (api.ProjectResponse, api.ProjectEnvironmentResponse, error) {
	var p api.ProjectResponse
	var e api.ProjectEnvironmentResponse
	if validateSegment(project, "project") != nil || !api.ValidProjectEnvironmentSlug(environment) {
		return p, e, invalid("invalid_argument", "a project slug and registered named environment are required")
	}
	var grant EnvironmentGrant
	if m.opts.Environments != nil {
		var ok bool
		grant, ok = m.opts.Environments.Named[project+"/"+environment]
		if !ok || (write && !grant.Write) {
			return p, e, invalid("environment_access_denied", "environment is outside this connection's consent")
		}
	}
	var err error
	p, err = m.client.GetProject(ctx, project)
	if err != nil {
		return p, e, err
	}
	e, err = m.client.GetProjectEnvironment(ctx, project, environment)
	if err != nil {
		return p, e, err
	}
	if p.ID == "" || p.Slug != project || e.ID == "" || e.ProjectID != p.ID || e.Slug != environment {
		return p, e, invalid("invalid_response", "API returned inconsistent environment identity")
	}
	if m.opts.Environments != nil && (grant.ProjectID != p.ID || grant.EnvironmentID != e.ID) {
		return p, e, invalid("environment_access_denied", "project or environment identity changed; reconnect to review access")
	}
	if wholeProject || m.opts.Environments != nil {
		for _, w := range p.Workloads {
			if !wholeProject && grant.Apps[w.Slug] == "" {
				continue
			}
			if validateSegment(w.Slug, "app") != nil {
				return p, e, invalid("invalid_response", "invalid workload identity")
			}
			if m.opts.AllowedApps != nil && m.opts.AllowedApps[w.Slug] == "" {
				return p, e, invalid("app_access_denied", "project operation requires consent for every workload")
			}
			if m.opts.Environments != nil && grant.Apps[w.Slug] == "" {
				return p, e, invalid("app_access_denied", "project workloads changed since environment consent; reconnect")
			}
			a, err := m.client.GetApp(ctx, w.Slug)
			if err != nil {
				return p, e, err
			}
			if a.ID == "" || !m.appIDAllowed(a.ID) || (m.opts.Environments != nil && grant.Apps[w.Slug] != a.ID) {
				return p, e, invalid("app_access_denied", "workload identity changed; reconnect to review access")
			}
		}
	}
	return p, e, nil
}

// Default app tools expose default telemetry. Named release IDs must also pass
// the registry and per-workload checks, even on an ID-only inspection.
func (m *management) deploymentAccess(ctx context.Context, d api.DeploymentResponse) error {
	if !m.appIDAllowed(d.AppID) {
		return invalid("app_access_denied", "deployment is outside this connection's consent")
	}
	if m.opts.Environments == nil || normalizedScope(d.Scope) == api.DefaultEnvScope {
		return nil
	}
	for _, g := range m.opts.Environments.Named {
		if g.Environment != d.Scope {
			continue
		}
		for slug, id := range g.Apps {
			if id != d.AppID {
				continue
			}
			p, _, err := m.environmentAccess(ctx, g.Project, g.Environment, false, false)
			if err != nil {
				return err
			}
			for _, w := range p.Workloads {
				if w.Slug != slug {
					continue
				}
				a, err := m.client.GetApp(ctx, slug)
				if err != nil {
					return err
				}
				if a.ID == id {
					return nil
				}
			}
		}
	}
	return invalid("environment_access_denied", "deployment environment is outside this connection's consent")
}
