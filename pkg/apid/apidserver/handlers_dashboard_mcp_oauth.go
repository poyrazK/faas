package apidserver

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mcpcatalog"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardMCPOAuthCookie = "gregale_mcp_oauth_flow"

type dashboardMCPOAuthFlow struct {
	Kind, Account, Session, App, Slug, Role, Revision, State string
	Expires                                                  time.Time
}

func (s *server) dashboardMCPOAuthRedirect() string {
	target := normalizeCLIAuthURLBase(s.cliAuthURLBase) + api.MCPCatalogOAuthBrowserCallbackPath
	if !api.ValidMCPOAuthBrowserRedirect(target) {
		return ""
	}
	return target
}

func dashboardMCPOAuthProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		http.Error(w, "Connection not found", http.StatusNotFound)
	case errors.Is(err, state.ErrConflict), errors.Is(err, state.ErrMCPCatalogMonitorChanged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "OAuth recovery refused", "Reload the connection, resolve uncertain issuance at the provider, or wait for the current operation"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("Invalid OAuth recovery request or callback configuration"))
	default:
		api.WriteProblem(w, api.ErrCapacity("OAuth recovery is unavailable; inspect connection health before retrying"))
	}
}

func dashboardMCPOAuthHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
}

func (s *server) dashboardMCPOAuthAction(w http.ResponseWriter, r *http.Request) {
	dashboardMCPOAuthHeaders(w)
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	session, ok := authmw.SessionFromContext(r)
	if !ok || session.ID == "" {
		dashboardMCPOAuthProblem(w, state.ErrConflict)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := middleware.VerifyAuthenticatedNamed(s.sessions, r, dashboardMCPRecoveryAction, account.ID, dashboardMCPRecoveryCookie); err != nil {
		api.WriteProblem(w, api.ErrValidation("Invalid recovery CSRF token; reload the dashboard"))
		return
	}
	if err := r.ParseForm(); err != nil {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	for key, values := range r.PostForm {
		if len(values) != 1 || (key != "csrf_token" && key != "role" && key != "revision" && key != "action" && key != "provider_confirmed") {
			dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
			return
		}
	}
	action, role, revision := r.PostForm.Get("action"), r.PostForm.Get("role"), r.PostForm.Get("revision")
	if !api.ValidCatalogMonitorRole(role) || revision == "" || (action != "begin" && action != "retry_renewal") {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.MCPCatalogObserverMaxTimeoutSeconds)*time.Second)
	defer cancel()
	app, err := s.store.AppBySlug(ctx, r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID || app.Status == state.AppDeleted {
		dashboardMCPOAuthProblem(w, state.ErrNotFound)
		return
	}
	if !app.AcceptsRequestInvocations() || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || !account.Plan.CustomMetricsAllowed() {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	if action == "retry_renewal" {
		source, ok := s.store.(state.MCPCatalogOAuthRecoveryStore)
		if !ok {
			dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
			return
		}
		_, err := source.RetryMCPCatalogOAuthRenewal(ctx, account.ID, app.ID, role, revision)
		if err != nil {
			dashboardMCPOAuthProblem(w, err)
			return
		}
		http.Redirect(w, r, dashboardMCPURL(url.Values{"app": {app.Slug}}, "notice", "renewal_scheduled"), http.StatusSeeOther)
		return
	}
	redirect := s.dashboardMCPOAuthRedirect()
	runner, closeTransport := mcpcatalog.NewOAuthRunner(s.store, hostIdentitiesForUnseal)
	defer closeTransport()
	out, err := runner.BrowserOAuth(ctx, account.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "begin", Role: role, Revision: revision, RedirectURI: redirect}, redirect, session.ID, r.PostForm.Get("provider_confirmed") == "yes")
	if err != nil {
		dashboardMCPOAuthProblem(w, err)
		return
	}
	if out.State != "authorization_pending" || out.AuthorizationURL == "" || len(out.CallbackState) != 43 {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	flow := dashboardMCPOAuthFlow{Kind: "mcp_browser_oauth", Account: account.ID, Session: session.ID, App: app.ID, Slug: app.Slug, Role: role, Revision: out.Revision, State: out.CallbackState, Expires: time.Now().Add(time.Duration(api.MCPCatalogOAuthFlowSeconds) * time.Second)}
	payload, err := json.Marshal(flow)
	if err != nil {
		dashboardMCPOAuthProblem(w, err)
		return
	}
	sealed, err := s.sessions.SealForCSRF(payload)
	if err != nil {
		dashboardMCPOAuthProblem(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardMCPOAuthCookie, Value: sealed, Path: api.MCPCatalogOAuthBrowserCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: api.MCPCatalogOAuthFlowSeconds})
	http.Redirect(w, r, out.AuthorizationURL, http.StatusSeeOther)
}

// Authenticate callbacks without copying authorization codes into login/MFA
// return URLs. Restore the query only inside the authenticated handler.
func (s *server) dashboardMCPOAuthCallbackAuth(w http.ResponseWriter, r *http.Request) {
	dashboardMCPOAuthHeaders(w)
	original := *r.URL
	safe := r.Clone(r.Context())
	safeURL := *r.URL
	safeURL.RawQuery = ""
	safe.URL = &safeURL
	safe.RequestURI = safeURL.RequestURI()
	s.sessionAuth(http.HandlerFunc(func(w http.ResponseWriter, authenticated *http.Request) {
		authenticated.URL = &original
		s.dashboardMCPOAuthCallback(w, authenticated)
	})).ServeHTTP(w, safe)
}

func (s *server) dashboardMCPOAuthCallback(w http.ResponseWriter, r *http.Request) {
	account, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	session, ok := authmw.SessionFromContext(r)
	if !ok {
		dashboardMCPOAuthProblem(w, state.ErrConflict)
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	for _, values := range query {
		if len(values) != 1 {
			dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
			return
		}
	}
	cookie, err := r.Cookie(dashboardMCPOAuthCookie)
	if err != nil || len(cookie.Value) > 4096 || s.sessions == nil {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	payload, err := s.sessions.OpenForCSRF(raw)
	if err != nil {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	var flow dashboardMCPOAuthFlow
	if json.Unmarshal(payload, &flow) != nil || flow.Kind != "mcp_browser_oauth" || flow.Account != account.ID || flow.Session != session.ID || len(flow.State) != 43 || subtle.ConstantTimeCompare([]byte(flow.State), []byte(query.Get("state"))) != 1 {
		dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: dashboardMCPOAuthCookie, Path: api.MCPCatalogOAuthBrowserCallbackPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	notice := "authorization_expired"
	if flow.Expires.After(time.Now()) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.MCPCatalogObserverMaxTimeoutSeconds)*time.Second)
		defer cancel()
		app, e := s.store.AppByID(ctx, flow.App)
		if e != nil || app.AccountID != account.ID || app.Slug != flow.Slug || app.Status == state.AppDeleted {
			dashboardMCPOAuthProblem(w, state.ErrNotFound)
			return
		}
		if !app.AcceptsRequestInvocations() || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || !account.Plan.CustomMetricsAllowed() {
			dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
			return
		}
		if (query.Get("code") == "") == (query.Get("error") == "") {
			dashboardMCPOAuthProblem(w, state.ErrInvalidArgument)
			return
		}
		runner, closeTransport := mcpcatalog.NewOAuthRunner(s.store, hostIdentitiesForUnseal)
		defer closeTransport()
		out, e := runner.BrowserOAuth(ctx, account.ID, app.ID, api.MCPCatalogOAuthRequest{Action: "complete", Role: flow.Role, Revision: flow.Revision, State: flow.State, Code: query.Get("code"), Denied: query.Get("error") != ""}, s.dashboardMCPOAuthRedirect(), session.ID, false)
		notice = out.State
		if e == nil && notice == "authorization_denied" && query.Get("error") != "access_denied" {
			notice = "authorization_failed"
		}
		if e != nil {
			notice = "authorization_failed"
			if out.State == "authorization_uncertain" || out.State == "authorization_expired" {
				notice = out.State
			}
		}
	}
	http.Redirect(w, r, dashboardMCPURL(url.Values{"app": {flow.Slug}}, "notice", notice), http.StatusSeeOther)
}
