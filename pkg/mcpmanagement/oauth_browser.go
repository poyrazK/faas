package mcpmanagement

import (
	"context"
	"crypto/subtle"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const flowCookieName = "__Host-gregale-mcp-flow"

var consentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Connect to Gregale</title></head>
<body><main><h1>Connect to Gregale</h1><p>Client: <strong>{{.Client}}</strong></p><p>Return address: {{.Redirect}}</p><p>Gregale API: {{.APIBase}}</p>
{{if .Account}}<p>Account: <strong>{{.Account}}</strong></p><p>This client can inspect apps, deployments, logs, metrics, health and routes in this account. App logs may contain private data.</p>{{if .Capabilities}}<p>With write access it may also:</p><ul>{{range .Capabilities}}<li>{{.}}</li>{{end}}</ul>{{end}}
<form method="post" action="{{.ConsentAction}}"><input type="hidden" name="request_id" value="{{.RequestID}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
{{if .Writes}}<p><label><input type="checkbox" name="allow_writes" value="yes" required> Also allow reviewed deployments, production promotion, and checked rollback. These actions can change traffic and incur costs.</label></p>{{end}}
<fieldset><legend>Apps this agent may access</legend><label><input type="radio" name="app_access" value="all" required> All apps in this account, including future apps</label><br><label><input type="radio" name="app_access" value="selected" required> Only selected apps</label>{{range .Apps}}<p><label><input type="checkbox" name="apps" value="{{.Slug}}"> {{.Slug}}</label></p>{{end}}</fieldset>
<fieldset><legend>App diagnostics (optional)</legend><p>Allow retained request status, routes, timing, performance evidence, retained wake observations, trace-correlated invocation lifecycle metadata, sampled CPU profile function labels and collection/attribution summaries, and scheduler residency causes across all environments of each selected app. This may contain private application data. Future apps are excluded. Native plan and retention limits still apply. This does not grant environment, log or write access.</p>{{range .Apps}}<p><label><input type="checkbox" name="request_read_apps" value="{{.Slug}}"> Read app diagnostics for {{.Slug}}</label></p>{{end}}</fieldset>
{{if .Writes}}<fieldset><legend>Regression triage (optional)</legend><p>Allow reviewed acknowledge, dismiss, resolve and reopen actions for selected apps. Select diagnostic read access above and write access to each target environment below. These actions update triage metadata.</p>{{range .Apps}}<p><label><input type="checkbox" name="regression_write_apps" value="{{.Slug}}"> Triage regressions for {{.Slug}}</label></p>{{end}}</fieldset>{{end}}
<fieldset><legend>Environment access</legend><p>Default app inspection is included. Named environments require separate consent for the selected apps.</p>{{if .Writes}}<p><label><input type="checkbox" name="default_write" value="yes"> Allow writes to the default environment</label></p>{{end}}{{range .Environments}}<p><label><input type="checkbox" name="environments" value="{{.Project}}/{{.Environment}}"> Read {{.Project}} / {{.Environment}}</label>{{if $.Writes}} <label><input type="checkbox" name="environment_writes" value="{{.Project}}/{{.Environment}}"> Write {{.Project}} / {{.Environment}}</label>{{end}}</p>{{end}}</fieldset>
<p>Environment promotions affect every project workload and require consent for all of them. Protected targets still need a human-issued Gregale approval. The connection lasts at most eight hours. Revoke it through your client or revoke its credential in Gregale.</p>
<button name="decision" value="approve" type="submit">Connect this account</button> <button name="decision" value="deny" type="submit" formnovalidate>Cancel</button></form>
{{else if .Native}}<p>Sign in to Gregale to choose your account. Then return here to review the agent's access.</p>
{{if .LoginURL}}<p><a href="{{.LoginURL}}" target="_blank" rel="noopener noreferrer">Sign in to Gregale</a></p>{{end}}
<form method="post" action="{{.ConsentAction}}"><input type="hidden" name="request_id" value="{{.RequestID}}"><input type="hidden" name="csrf" value="{{.CSRF}}">{{if and .Writes (not .LoginURL)}}<fieldset><legend>With write access, also allow (optional)</legend><label><input type="checkbox" name="capability" value="secrets:request"> Asking you to enter secrets on a Gregale page; the agent never sees their values</label><br><label><input type="checkbox" name="capability" value="storage:manage"> Creating object storage buckets and binding them to apps</label><br><label><input type="checkbox" name="capability" value="postgres:manage"> Creating managed PostgreSQL databases and binding them to apps (billed)</label></fieldset>{{end}}<button name="decision" value="{{if .LoginURL}}check-login{{else}}start-login{{end}}" type="submit">{{if .LoginURL}}I authorized in Gregale; review account{{else}}Start Gregale sign-in{{end}}</button><button name="decision" value="deny" type="submit">Cancel</button></form>
{{else}}<p>Enter a Gregale API key for the account you want to connect. Use a dedicated key with the smallest needed API scopes. The next screen identifies the account before you approve access.</p>
<form method="post" action="{{.ConsentAction}}" autocomplete="off"><input type="hidden" name="request_id" value="{{.RequestID}}"><input type="hidden" name="csrf" value="{{.CSRF}}">
<p><label>Gregale API key <input name="api_key" type="password" required autocomplete="off" maxlength="4096"></label></p>
<button name="decision" value="verify" type="submit">Review account</button> <button name="decision" value="deny" type="submit" formnovalidate>Cancel</button></form>{{end}}
</main></body></html>`))

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
	if len(r.requests) >= maxOAuthEntries {
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
	_ = consentPage.Execute(w, view)
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
	server, management, err := newManagementServer(g.client, Options{Lifecycle: r.opts.Lifecycle, Telemetry: r.opts.Telemetry, auditLogger: r.opts.AuditLogger, accountID: g.accountID, grantID: g.id, AllowedApps: apps, Environments: environments, planStore: r.opts.StateStore, planNamespace: g.id, Profile: "oauth", APIBase: r.opts.APIBase, Version: r.opts.Version, Timeout: r.opts.Timeout, Token: g.client.Token(), AllowWrites: r.opts.AllowWrites && slices.Contains(g.scopes, scopeWrite), Toolsets: r.opts.Toolsets, Capabilities: g.capabilities, LocalCLI: r.grantCLI(g.client.Token())})
	if err != nil {
		oauthError(w, 500, "server_error", "Could not prepare account tools")
		return
	}
	code, err := randomToken("gmcp_code_")
	if err != nil {
		oauthError(w, 500, "server_error", "Could not authorize account connection")
		return
	}
	if len(r.codes) >= maxOAuthEntries {
		oauthError(w, 429, "temporarily_unavailable", "Authorization code capacity reached")
		return
	}
	g.server, g.management = server, management
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
