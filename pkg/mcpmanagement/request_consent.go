package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func selectedRequestApps(req *http.Request, available []api.AppResponse, allowed map[string]string) (map[string]string, error) {
	selected := req.PostForm["request_read_apps"]
	if len(selected) > maxOAuthEntries {
		return nil, errors.New("too many request diagnostic selections")
	}
	out := map[string]string{}
	for _, slug := range selected {
		id := ""
		for _, app := range available {
			if app.Slug == slug {
				if id != "" && id != app.ID {
					return nil, errors.New("ambiguous reviewed app")
				}
				id = app.ID
			}
		}
		if validateSegment(slug, "app") != nil || validateSegment(id, "app_id") != nil || (allowed != nil && allowed[slug] != id) {
			return nil, errors.New("request diagnostic app was not reviewed or allowed")
		}
		out[slug] = id
	}
	return out, nil
}

func (m *management) requestReadAllowed(slug, id string) bool {
	return id != "" && m.appIDAllowed(id) && (m.opts.Environments == nil || m.opts.Environments.RequestReadApps[slug] == id)
}

func (m *management) requestApp(ctx context.Context, slug string) (api.AppResponse, error) {
	var app api.AppResponse
	if err := validateSegment(slug, "app"); err != nil {
		return app, err
	}
	// Deny before fetching diagnostics, even for account-wide/future app grants.
	if m.opts.Environments != nil && m.opts.Environments.RequestReadApps[slug] == "" {
		return app, invalid("request_access_denied", "app-wide diagnostics require separate per-app consent; reconnect to review access")
	}
	if err := m.authorizeArgs(ctx, struct {
		App string `json:"app"`
	}{slug}); err != nil {
		return app, err
	}
	app, err := m.client.GetApp(ctx, slug)
	if err != nil {
		return app, err
	}
	if app.Slug != slug || !m.requestReadAllowed(slug, app.ID) {
		return app, invalid("request_access_denied", "diagnostic app identity changed or is outside consent")
	}
	return app, nil
}

func (m *management) requestAccessMetadata() any {
	mode := "native_connection"
	apps := map[string]string{}
	if m.opts.Environments != nil {
		mode = "not_consented"
		if len(m.opts.Environments.RequestReadApps) > 0 {
			mode = "selected_apps"
		}
		keys := []string{}
		for slug := range m.opts.Environments.RequestReadApps {
			keys = append(keys, slug)
		}
		sort.Strings(keys)
		for _, slug := range keys[:min(len(keys), defaultPageSize)] {
			apps[slug] = m.opts.Environments.RequestReadApps[slug]
		}
		return map[string]any{"mode": mode, "apps": apps, "selected_app_count": len(keys), "apps_truncated": len(keys) > len(apps), "scope": "app_wide_retained_diagnostics", "basis": "consent_only"}
	}
	return map[string]any{"mode": mode, "scope": "app_wide_retained_diagnostics", "basis": "native_authorization_required"}
}
