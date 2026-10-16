package apidserver

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type routeDiagnosticSelection struct {
	Scope       string
	Environment state.ProjectEnvironment
}

// Separate report windows from environment selection; telemetryEnvironment
// itself deliberately accepts only one selector and no unrelated parameters.
func (s *server) routeDiagnosticEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, advice bool) (state.App, routeDiagnosticSelection, *api.Problem) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return app, routeDiagnosticSelection{}, api.ErrValidation("malformed route diagnostic query")
	}
	for key, values := range q {
		if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] || (key != "since" && key != "until" && key != "scope" && key != "environment" && (!advice || key != "cache_max_age")) {
			return app, routeDiagnosticSelection{}, api.ErrValidation("invalid or repeated route diagnostic parameter")
		}
	}
	selector := r.Clone(r.Context())
	urlCopy := *r.URL
	selector.URL = &urlCopy
	q.Del("since")
	q.Del("until")
	q.Del("cache_max_age")
	selector.URL.RawQuery = q.Encode()
	resolved, env, scope, _, problem := s.telemetryEnvironment(w, selector, acct, app)
	return resolved, routeDiagnosticSelection{Scope: scope, Environment: env}, problem
}

func (selection routeDiagnosticSelection) identity(app state.App) api.RouteDiagnosticsIdentity {
	if selection.Scope == "" {
		return api.RouteDiagnosticsIdentity{}
	}
	return api.RouteDiagnosticsIdentity{AppID: app.ID, Scope: selection.Scope, ProjectID: selection.Environment.ProjectID, EnvironmentID: selection.Environment.ID, Coverage: "retained_deployment_history", Partial: true}
}
