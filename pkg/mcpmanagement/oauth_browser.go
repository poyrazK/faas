package mcpmanagement

import (
	"context"
	"crypto/subtle"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const flowCookieName = "__Host-gregale-mcp-flow"

type consentView struct {
	Client   string
	Redirect string
	APIBase  string
	Account  string
	// ConsentAction is the consent endpoint, under /mcp on the hosted bridge.
	ConsentAction string
	// Capabilities describe the extra scopes chosen before sign-in.
	Capabilities []string
	RequestID    string
	CSRF         string
	Writes       bool
	Native       bool
	LoginURL     string
	Apps         []consentApp
	Environments []EnvironmentGrant
}

func (r *remoteServer) authorize(w http.ResponseWriter, req *http.Request) {
	query := req.URL.Query()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	c := r.clients[query.Get("client_id")]
	scopes, valid := r.requestedScopes(query.Get("scope"))
	if c == nil || !slices.Contains(c.Redirects, query.Get("redirect_uri")) || query.Get("response_type") != "code" || query.Get("resource") != r.opts.ResourceURL || query.Get("code_challenge_method") != "S256" || !pkceChallenge.MatchString(query.Get("code_challenge")) || !valid || len(query.Get("state")) > 2048 {
		oauthError(w, 400, "invalid_request", "Registered redirect URI, exact resource, supported scopes, code response type and S256 PKCE are required")
		return
	}
	if len(r.requests) >= r.capacity() {
		oauthError(w, 429, "temporarily_unavailable", "Authorization capacity reached")
		return
	}
	id, csrf, cookie, err := newFlowTokens()
	if err != nil {
		oauthError(w, 500, "server_error", "Could not begin authorization")
		return
	}
	flow := &authorizationRequest{ID: id, ClientID: c.ID, Redirect: query.Get("redirect_uri"), State: query.Get("state"), Challenge: query.Get("code_challenge"), Scopes: scopes, CSRFHash: tokenHash(csrf), CookieHash: tokenHash(cookie), Expires: time.Now().Add(oauthFlowTTL)}
	r.requests[id] = flow
	http.SetCookie(w, &http.Cookie{Name: flowCookieName, Value: cookie, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(oauthFlowTTL.Seconds())})
	r.renderConsent(w, flow, csrf, "")
}

func newFlowTokens() (string, string, string, error) {
	id, err := randomToken("gmcp_flow_")
	if err != nil {
		return "", "", "", err
	}
	csrf, err := randomToken("gmcp_csrf_")
	if err != nil {
		return "", "", "", err
	}
	cookie, err := randomToken("gmcp_browser_")
	return id, csrf, cookie, err
}

func (r *remoteServer) renderConsent(w http.ResponseWriter, flow *authorizationRequest, csrf, account string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	client := r.clients[flow.ClientID]
	if client == nil {
		oauthError(w, 400, "access_denied", "Client registration expired; start a new connection")
		return
	}
	view := consentView{ConsentAction: r.issuer + "/oauth/consent", Capabilities: describeCapabilities(flow.Capabilities), Client: client.Name, Redirect: flow.Redirect, APIBase: r.opts.APIBase, Account: account, RequestID: flow.ID, CSRF: csrf, Writes: slices.Contains(flow.Scopes, scopeWrite), Native: r.opts.Onboarding == "browser", LoginURL: flow.NativeURL}
	if g := r.grants[flow.GrantID]; g != nil {
		view.Environments = g.availableEnvironments
		for _, app := range g.availableApps {
			view.Apps = append(view.Apps, consentApp{Slug: app.Slug})
		}
	}
	_, _ = io.WriteString(w, consentPage(view))
}

func (r *remoteServer) consent(w http.ResponseWriter, req *http.Request) {
	if req.Header.Get("Origin") != r.origin || req.ParseForm() != nil {
		oauthError(w, 403, "access_denied", "Consent must come from this authorization page")
		return
	}
	r.mu.Lock()
	r.pruneLocked()
	flow := r.requests[req.PostForm.Get("request_id")]
	cookie, err := req.Cookie(flowCookieName)
	if flow == nil || err != nil || subtle.ConstantTimeCompare([]byte(flow.CookieHash), []byte(tokenHash(cookie.Value))) != 1 || subtle.ConstantTimeCompare([]byte(flow.CSRFHash), []byte(tokenHash(req.PostForm.Get("csrf")))) != 1 {
		r.mu.Unlock()
		oauthError(w, 403, "access_denied", "Invalid or expired browser authorization")
		return
	}
	decision := req.PostForm.Get("decision")
	if decision == "start-login" || decision == "check-login" {
		copyFlow := *flow
		r.mu.Unlock()
		r.nativeLogin(w, req, &copyFlow, decision)
		return
	}
	if decision == "verify" && r.opts.Onboarding == "api-key" {
		copyFlow := *flow
		r.mu.Unlock()
		r.verifyAccount(w, req, &copyFlow)
		return
	}
	defer r.mu.Unlock()
	switch req.PostForm.Get("decision") {
	case "deny":
		r.finishAuthorization(w, req, flow, "", "access_denied")
	case "approve":
		r.approveLocked(w, req, flow)
	default:
		oauthError(w, 400, "invalid_request", "Choose review, connect, or cancel")
	}
}

func (r *remoteServer) verifyAccount(w http.ResponseWriter, req *http.Request, flow *authorizationRequest) {
	key := req.PostForm.Get("api_key")
	if flow.GrantID != "" || len(key) == 0 || len(key) > 4096 || strings.ContainsAny(key, " \r\n\t") {
		oauthError(w, 400, "invalid_request", "Enter a valid dedicated Gregale API key")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), r.opts.Timeout)
	defer cancel()
	client := r.apiClient(key)
	account, err := client.Whoami(ctx)
	if err != nil || account.ID == "" {
		oauthError(w, 401, "access_denied", "Gregale could not verify this account credential")
		return
	}
	r.prepareAccount(w, req, flow, client, account.ID, time.Now().Add(oauthGrantTTL))
}

func (r *remoteServer) approveLocked(w http.ResponseWriter, req *http.Request, flow *authorizationRequest) {
	g := r.grants[flow.GrantID]
	if g == nil || g.revoked || (slices.Contains(g.scopes, scopeWrite) && req.PostForm.Get("allow_writes") != "yes") {
		oauthError(w, 400, "access_denied", "Review the account and explicitly approve requested write access")
		return
	}
	apps, err := selectedApps(req, g.availableApps)
	if err != nil {
		oauthError(w, 400, "invalid_request", "Choose account-wide access or one or more reviewed apps")
		return
	}
	environments, err := selectedEnvironments(req, g.availableEnvironments, apps, r.opts.AllowWrites && slices.Contains(g.scopes, scopeWrite))
	if err != nil {
		oauthError(w, 400, "invalid_request", "Choose reviewed environments and explicitly allow each write target")
		return
	}
	environments.RequestReadApps, err = selectedRequestApps(req, g.availableApps, apps)
	if err != nil {
		oauthError(w, 400, "invalid_request", "Choose request diagnostics only for reviewed, accessible apps")
		return
	}
	environments.RegressionWriteApps, err = selectedRegressionWriteApps(req, environments.RequestReadApps, r.opts.AllowWrites && slices.Contains(g.scopes, scopeWrite))
	if err != nil {
		oauthError(w, 400, "invalid_request", "Regression triage requires explicit write scope and diagnostic access to each selected app")
		return
	}
	// Building the server here checks the consent; the first request uses it.
	server, management, err := newManagementServer(g.client, r.grantOptions(g, apps, environments))
	if err != nil {
		oauthError(w, 500, "server_error", "Could not prepare account tools")
		return
	}
	code, err := randomToken("gmcp_code_")
	if err != nil {
		oauthError(w, 500, "server_error", "Could not authorize account connection")
		return
	}
	if len(r.codes) >= r.capacity() {
		oauthError(w, 429, "temporarily_unavailable", "Authorization code capacity reached")
		return
	}
	g.server, g.management, g.approved, g.lastUsed = server, management, true, time.Now()
	g.allowedApps, g.environments = apps, environments
	g.expires = minTime(g.credentialExpires, time.Now().Add(oauthGrantTTL))
	g.availableApps = nil
	g.availableEnvironments = nil
	r.codes[tokenHash(code)] = &authorizationCode{ClientID: flow.ClientID, Redirect: flow.Redirect, Challenge: flow.Challenge, GrantID: g.id, Expires: time.Now().Add(oauthCodeTTL)}
	r.audit("mcp.grant.approved", g)
	r.finishAuthorization(w, req, flow, code, "")
}

func (r *remoteServer) finishAuthorization(w http.ResponseWriter, req *http.Request, flow *authorizationRequest, code, errorCode string) {
	delete(r.requests, flow.ID)
	if errorCode != "" {
		if g := r.grants[flow.GrantID]; g != nil {
			r.revokeLocked(g)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookieName, Value: "", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	redirect, _ := url.Parse(flow.Redirect)
	query := redirect.Query()
	query.Set("state", flow.State)
	if errorCode != "" {
		query.Set("error", errorCode)
	} else {
		query.Set("code", code)
	}
	redirect.RawQuery = query.Encode()
	http.Redirect(w, req, redirect.String(), http.StatusSeeOther)
}
