package mcpmanagement

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type consentApp struct{ Slug string }

func (r *remoteServer) nativeLogin(w http.ResponseWriter, req *http.Request, flow *authorizationRequest, decision string) {
	if r.opts.Onboarding != "browser" || flow.GrantID != "" {
		oauthError(w, 400, "invalid_request", "Browser sign-in is unavailable for this request")
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), r.opts.Timeout)
	defer cancel()
	client := r.apiClient("")
	if decision == "start-login" {
		r.startNativeLogin(ctx, w, req, flow, client)
		return
	}
	if flow.NativeCode == "" {
		oauthError(w, 400, "invalid_request", "Start Gregale sign-in first")
		return
	}
	result, err := client.ExchangeCliAuthCode(ctx, flow.NativeCode)
	var apiErr *api.APIError
	if errors.As(err, &apiErr) && apiErr.Problem.Code == api.CodeCliAuthPending {
		r.mu.Lock()
		defer r.mu.Unlock()
		if active := r.requests[flow.ID]; active != nil {
			r.renderConsent(w, active, req.PostForm.Get("csrf"), "")
			return
		}
	}
	expiry, expiryErr := time.Parse(time.RFC3339, result.ExpiresAt)
	if err != nil || expiryErr != nil || result.Plaintext == "" || result.Account.ID == "" || !expiry.After(time.Now()) {
		oauthError(w, 401, "access_denied", "Gregale sign-in expired or could not be verified; start a new connection")
		return
	}
	r.prepareAccount(w, req, flow, r.apiClient(result.Plaintext), result.Account.ID, minTime(expiry, time.Now().Add(oauthGrantTTL)))
}

func (r *remoteServer) startNativeLogin(ctx context.Context, w http.ResponseWriter, req *http.Request, flow *authorizationRequest, client *api.Client) {
	if flow.NativeCode != "" {
		oauthError(w, 400, "invalid_request", "Sign-in already started")
		return
	}
	code, err := client.MintMCPAuthCode(ctx, slices.Contains(flow.Scopes, scopeWrite))
	login, parseErr := url.Parse(code.URL)
	// The API controls its console origin; never render credential-bearing,
	// script or arbitrary non-browser redirect schemes.
	if err != nil || parseErr != nil || login.Host == "" || login.User != nil || login.Fragment != "" || (login.Scheme != "https" && (login.Scheme != "http" || !loopbackHost(login.Hostname()))) || code.Code == "" {
		oauthError(w, 502, "server_error", "Gregale could not start browser sign-in")
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	active := r.requests[flow.ID]
	if active == nil || active.NativeCode != "" {
		oauthError(w, 400, "access_denied", "Browser authorization expired or was already started")
		return
	}
	active.NativeCode, active.NativeURL = code.Code, code.URL
	r.renderConsent(w, active, req.PostForm.Get("csrf"), "")
}

func (r *remoteServer) prepareAccount(w http.ResponseWriter, req *http.Request, flow *authorizationRequest, client *api.Client, accountID string, expiry time.Time) {
	ctx, cancel := context.WithTimeout(req.Context(), r.opts.Timeout)
	defer cancel()
	account, err := client.Whoami(ctx)
	if err != nil || account.ID != accountID {
		oauthError(w, 401, "access_denied", "Gregale could not verify this account credential")
		return
	}
	apps, err := client.ListApps(ctx)
	if err != nil {
		oauthError(w, 403, "access_denied", "Credential must permit app inspection")
		return
	}
	environments, err := discoverEnvironments(ctx, client)
	if err != nil {
		oauthError(w, 502, "server_error", "Gregale could not load environments for consent")
		return
	}
	// Retain identities only, never app manifests/env/telemetry in OAuth state.
	for i, app := range apps {
		apps[i] = api.AppResponse{ID: app.ID, Slug: app.Slug}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked()
	active := r.requests[flow.ID]
	if active == nil || active.GrantID != "" {
		oauthError(w, 400, "access_denied", "Browser authorization expired or was already reviewed")
		return
	}
	if len(r.grants) >= maxOAuthEntries {
		oauthError(w, 429, "temporarily_unavailable", "Authorization grant capacity reached")
		return
	}
	id, err := randomToken("gmcp_grant_")
	if err != nil {
		oauthError(w, 500, "server_error", "Could not prepare account connection")
		return
	}
	g := &oauthGrant{id: id, clientID: flow.ClientID, accountID: account.ID, scopes: append([]string(nil), flow.Scopes...), client: client, expires: minTime(expiry, flow.Expires), credentialExpires: expiry, availableApps: apps, availableEnvironments: environments}
	r.grants[id], active.GrantID = g, id
	active.NativeCode, active.NativeURL = "", ""
	r.renderConsent(w, active, req.PostForm.Get("csrf"), account.ID)
}

func selectedApps(req *http.Request, apps []api.AppResponse) (map[string]string, error) {
	if req.PostForm.Get("app_access") == "all" {
		return nil, nil
	}
	selected := req.PostForm["apps"]
	if req.PostForm.Get("app_access") != "selected" || len(selected) == 0 || len(selected) > len(apps) {
		return nil, errors.New("invalid app selection")
	}
	allowed := map[string]string{}
	for _, slug := range selected {
		found := false
		for _, app := range apps {
			if app.Slug == slug && app.ID != "" {
				allowed[slug], found = app.ID, true
				break
			}
		}
		if !found || strings.TrimSpace(slug) != slug {
			return nil, errors.New("unknown app")
		}
	}
	return allowed, nil
}
