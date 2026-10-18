package appaccess

import (
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/state"
)

// These legacy resource URLs retain their creator-only permission boundary,
// but an app's creator must still be a current owner/admin of its company.
// An indirect URL never bypasses offboarding or grants a new shared action.
func indirectlyAddressedApp(r *http.Request, store Store) (state.App, bool, error) {
	var appID string
	var err error
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1/domains/") && r.PathValue("domain") != "":
		var domain state.CustomDomain
		domain, err = store.DomainByName(r.Context(), strings.ToLower(r.PathValue("domain")))
		appID = domain.AppID
	case strings.HasPrefix(r.URL.Path, "/v1/crons/") && r.PathValue("id") != "":
		var cron state.Cron
		cron, err = store.CronByID(r.Context(), r.PathValue("id"))
		appID = cron.AppID
	case strings.HasPrefix(r.URL.Path, "/v1/edge-rules/") && r.PathValue("id") != "":
		var rule state.EdgeRule
		rule, err = store.GetEdgeRuleByID(r.Context(), r.PathValue("id"))
		appID = rule.AppID
	case strings.HasPrefix(r.URL.Path, "/v1/invocations/") && r.PathValue("id") != "":
		var invocation state.Invocation
		invocation, err = store.InvocationByID(r.Context(), r.PathValue("id"))
		appID = invocation.AppID
	default:
		return state.App{}, false, nil
	}
	if err != nil {
		return state.App{}, true, err
	}
	if appID == "" {
		return state.App{}, false, nil // Account-scoped rules have no app.
	}
	app, err := store.AppByID(r.Context(), appID)
	return app, true, err
}
