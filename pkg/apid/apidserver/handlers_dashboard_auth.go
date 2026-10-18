// adr: 1190 — the dashboard's Auth screens run in the owner's browser and call
// the Auth app's /admin/v1 with an owner-held key. apid serves only the page:
// it never receives the key, and user records never pass through it.
package apidserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authsettings"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) renderDashboardAuth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), api.AppOperationalReadTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	acct, app, ok := s.dashboardDataAPIApp(w, r)
	if !ok {
		return
	}
	if !api.ApplicationAuthEnabled(acct.ID) {
		http.NotFound(w, r)
		return
	}
	changed := r.URL.Query().Get("changed")
	data := dashboard.AuthAdminData{Slug: app.Slug, Providers: authsettings.Providers, Changed: changed, Error: r.URL.Query().Get("error"), RestartPending: strings.HasSuffix(changed, "-restart-pending")}
	isAuth, err := s.appHasAuthBinding(ctx, acct, app)
	switch {
	case err != nil:
		data.Message = "Database binding status is unavailable. Try again shortly."
	case !isAuth:
		data.Message = "This app is not an application Auth app. Create one with `gregale auth create`."
	default:
		origin := s.dashboardAppOrigin(ctx, app)
		// The page may call exactly this origin; every other source keeps
		// the dashboard's default policy.
		if policy, ok := httpsec.CSPWithConnectOrigin(httpsec.NonceFromContext(ctx), origin); ok {
			w.Header().Set("Content-Security-Policy", policy)
			data.AuthOrigin = origin
			data.CSRFToken = s.issueDashboardConfigCSRF(w, s.log, dashboardAuthSettingsAction, dashboardAuthSettingsCookie, acct.ID, app.ID)
			data.Keys, data.MaxKeys, data.EmailProviders, data.SMSProviders = s.dashboardAuthKeys(ctx, acct, app), api.AuthMaxSigningKeys, authsettings.EmailProviders, authsettings.SMSProviders
		} else {
			data.Message = "This app has no HTTPS address the dashboard can reach."
		}
	}
	count, _ := s.store.CountDeployedApps(ctx, acct.ID)
	page := dashboard.Page{Title: "Auth — " + app.Slug, Body: "auth_admin", Account: dashboardAccountView(acct, count), Data: data}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) appHasAuthBinding(ctx context.Context, acct state.Account, app state.App) (bool, error) {
	if s.managedPostgresBindings == nil {
		return false, nil
	}
	bindings, err := s.managedPostgresBindings.ListForApp(ctx, acct.ID, app.ID, api.DefaultEnvScope)
	if err != nil {
		return false, err
	}
	for _, binding := range bindings {
		if binding.Access == string(managedpostgres.CredentialAuth) {
			return true, nil
		}
	}
	return false, nil
}

// dashboardAppOrigin matches the app's canonical URL in the API, which
// `gregale auth create` used as the Auth app's external URL.
func (s *server) dashboardAppOrigin(ctx context.Context, app state.App) string {
	if resolver, ok := s.store.(state.DefaultCustomDomainStore); ok {
		if domain, err := resolver.DefaultCustomDomain(ctx, app.ID); err == nil && domain != "" {
			return "https://" + strings.ToLower(domain)
		}
	}
	return appURLForDomain(app.Slug, s.domain)
}
