package mcpcatalog

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func OAuthHandler(store state.Store, loader httpkit.AppLoader, identities func(context.Context) []*age.X25519Identity) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		app, ok := loader.LoadApp(w, r, acct, r.PathValue("slug"))
		if !ok {
			return
		}
		var req api.MCPCatalogOAuthRequest
		if httpkit.DecodeJSONSized(r, &req, api.MCPCatalogOAuthMaxResponseBytes) != nil || !api.ValidCatalogMonitorRole(req.Role) {
			api.WriteProblem(w, api.ErrValidation("invalid OAuth connection request"))
			return
		}
		if req.Action != "disconnect" {
			principal, present := httpkit.PrincipalFrom(r)
			if !present || (principal.Key != nil && !httpkit.APIKeyCarriesScope(*principal.Key, api.ScopeAdmin) && !httpkit.APIKeyCarriesScope(*principal.Key, api.ScopeSecretsRead)) {
				api.WriteProblem(w, api.NewProblem(403, api.CodeForbidden, "Secret access required", "OAuth connection requires secrets:read or admin"))
				return
			}
			if !app.AcceptsRequestInvocations() || app.PublicAuthMode == state.AppPublicAuthModeInternalOnly || !acct.Plan.CustomMetricsAllowed() {
				api.WriteProblem(w, api.ErrValidation("OAuth observers require a public HTTP app on a monitoring plan"))
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(api.MCPCatalogObserverMaxTimeoutSeconds)*time.Second)
		defer cancel()
		if req.Action == "retry_renewal" {
			recovery, ok := store.(state.MCPCatalogOAuthRecoveryStore)
			if !ok {
				respondOAuth(w, nil, state.ErrInvalidArgument)
				return
			}
			out, err := recovery.RetryMCPCatalogOAuthRenewal(ctx, acct.ID, app.ID, req.Role, req.Revision)
			respondOAuth(w, out, err)
			return
		}
		runner, closeTransport := NewOAuthRunner(store, identities)
		defer closeTransport()
		out, err := runner.connectOAuth(ctx, acct.ID, app.ID, req)
		respondOAuth(w, out, err)
	}
}
func (r *ObserverRunner) connectOAuth(ctx context.Context, account, app string, req api.MCPCatalogOAuthRequest) (api.MCPCatalogOAuthResponse, error) {
	return r.connectOAuthBound(ctx, account, app, req, "", "")
}

func (r *ObserverRunner) connectOAuthBound(ctx context.Context, account, app string, req api.MCPCatalogOAuthRequest, browserRedirect, browserSession string) (api.MCPCatalogOAuthResponse, error) {
	var out api.MCPCatalogOAuthResponse
	monitors, ok := r.Store.(state.MCPCatalogMonitorStore)
	if !ok {
		return out, state.ErrInvalidArgument
	}
	store, ok := r.Store.(state.MCPCatalogOAuthStore)
	if !ok {
		return out, state.ErrInvalidArgument
	}
	root, err := monitors.GetMCPCatalogMonitor(ctx, account, app)
	if err != nil {
		return out, err
	}
	view, ok := api.CatalogMonitorRoleSnapshot(root, req.Role)
	if !ok || view.Config.Revision != req.Revision || view.Config.Observer == nil || view.Config.Observer.OAuth == nil || !view.Config.Observer.OAuth.Guided {
		return out, state.ErrMCPCatalogMonitorChanged
	}
	cfg := view.Config.Observer.OAuth
	if cfg.Validate() != nil {
		return out, state.ErrInvalidArgument
	}
	if req.Action == "check" {
		return r.checkOAuthCompatibility(ctx, cfg, req.Revision), nil
	}
	role := api.CatalogMonitorRole(req.Role)
	if req.Action == "disconnect" {
		var clientCiphertext []byte
		if cfg.ClientSecretRef != "" && cfg.ResolvedRevocationAuthMethod() != "none" {
			key := strings.TrimPrefix(cfg.ClientSecretRef, api.SecretRefPrefix)
			secret, e := r.Store.GetAppSecretInScope(ctx, account, app, api.MCPConnectionScope(view.Config.Scope), key)
			if e == nil && secret != nil && secret.AccountID == account && secret.AppID == app && secret.Key == key && secret.Scope == api.MCPConnectionScope(view.Config.Scope) && secret.ManagedPostgresBindingID == "" && secret.ManagedObjectStorageCredentialID == "" && secret.ManagedCredentialRef == "" {
				clientCiphertext = secret.Ciphertext
			}
		}
		view, err = store.ChangeMCPCatalogOAuth(ctx, account, app, role, req.Revision, "disconnect", "", nil, nil, clientCiphertext)
		provider := "not_required"
		if view.OAuthLifecycle != nil && view.OAuthLifecycle.Revocation != nil {
			provider = view.OAuthLifecycle.Revocation.State
		}
		return api.MCPCatalogOAuthResponse{State: "disconnected", Revision: view.Config.Revision, ProviderRevocation: provider}, err
	}
	if r.Identities == nil {
		return out, errors.New("credential storage unavailable")
	}
	ids := r.Identities(ctx)
	if len(ids) == 0 || ids[0] == nil {
		return out, errors.New("credential storage unavailable")
	}
	if req.Action == "begin" {
		if browserSession != "" {
			if cfg.BrowserRedirectURI != browserRedirect || req.RedirectURI != browserRedirect || !api.ValidMCPOAuthBrowserRedirect(browserRedirect) {
				return out, state.ErrInvalidArgument
			}
		} else if !oauthLoopbackRedirect(req.RedirectURI) || !matchesOAuthCallbackPort(req.RedirectURI, cfg.CallbackPort) {
			return out, state.ErrInvalidArgument
		}
		if cfg.DiscoveryURL != "" {
			checked := r.checkOAuthCompatibility(ctx, cfg, req.Revision)
			if checked.State == "unsupported" || checked.State == "unavailable" {
				return checked, nil
			}
		}
		nonce := make([]byte, 32)
		if _, err = rand.Read(nonce); err != nil {
			return out, err
		}
		callbackState := base64.RawURLEncoding.EncodeToString(nonce)
		if _, err = rand.Read(nonce); err != nil {
			return out, err
		}
		verifier := base64.RawURLEncoding.EncodeToString(nonce)
		envelope := secretbox.Envelope{"VERIFIER": verifier, "REDIRECT_URI": req.RedirectURI}
		if browserSession != "" {
			envelope["BROWSER_SESSION"] = browserSession
		}
		sealed, e := secretbox.Seal(ids[0].Recipient(), envelope)
		if e != nil {
			return out, e
		}
		flow := api.MCPCatalogOAuthFlow{StateSHA256: digest([]byte(callbackState)), Ciphertext: sealed, ExpiresAt: time.Now().UTC().Add(time.Duration(api.MCPCatalogOAuthFlowSeconds) * time.Second)}
		if browserSession != "" {
			flow.BrowserSessionSHA256 = digest([]byte(browserSession))
		}
		// Allow a small transaction-time margin while retaining a strict five-minute lifetime.
		flow.ExpiresAt = flow.ExpiresAt.Add(-time.Second)
		view, err = store.ChangeMCPCatalogOAuth(ctx, account, app, role, req.Revision, "begin", "", &flow, nil)
		if err != nil {
			return out, err
		}
		target, _ := url.Parse(cfg.AuthorizationEndpoint)
		query := target.Query()
		challenge := sha256.Sum256([]byte(verifier))
		query.Set("response_type", "code")
		query.Set("client_id", cfg.ClientID)
		query.Set("redirect_uri", req.RedirectURI)
		query.Set("state", callbackState)
		query.Set("code_challenge_method", "S256")
		query.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
		if scopes := cfg.RequestedScopes(); len(scopes) > 0 {
			query.Set("scope", strings.Join(scopes, " "))
		}
		if cfg.Profile == "google" {
			query.Set("access_type", "offline")
			query.Set("prompt", "consent")
		}
		if cfg.Profile == "oidc" {
			query.Set("prompt", "consent")
		}
		if cfg.Resource != "" {
			query.Set("resource", cfg.Resource)
		}
		target.RawQuery = query.Encode()
		return api.MCPCatalogOAuthResponse{Revision: view.Config.Revision, State: "authorization_pending", AuthorizationURL: target.String(), CallbackState: callbackState}, nil
	}
	if req.Action != "complete" || len(req.State) != 43 || (!req.Denied && !oauthTokenValue(req.Code)) {
		return out, state.ErrInvalidArgument
	}
	if view.ObserverStatus == nil || view.ObserverStatus.OAuthFlow == nil {
		return out, state.ErrMCPCatalogMonitorChanged
	}
	envelope, err := secretbox.OpenMulti(ids, view.ObserverStatus.OAuthFlow.Ciphertext)
	if err != nil {
		return out, errors.New("authorization unavailable")
	}
	if browserSession != "" {
		if envelope["BROWSER_SESSION"] != browserSession || envelope["REDIRECT_URI"] != browserRedirect || cfg.BrowserRedirectURI != browserRedirect || !api.ValidMCPOAuthBrowserRedirect(browserRedirect) {
			return out, state.ErrMCPCatalogMonitorChanged
		}
	} else if envelope["BROWSER_SESSION"] != "" || !oauthLoopbackRedirect(envelope["REDIRECT_URI"]) {
		return out, state.ErrMCPCatalogMonitorChanged
	}
	view, err = store.ChangeMCPCatalogOAuth(ctx, account, app, role, req.Revision, "consume", digest([]byte(req.State)), nil, nil)
	if err != nil {
		return out, err
	}
	if req.Denied {
		if browserSession != "" {
			view, err = store.ChangeMCPCatalogOAuth(ctx, account, app, role, req.Revision, "abort", digest([]byte(req.State)), nil, nil)
			return api.MCPCatalogOAuthResponse{State: "authorization_denied", Revision: view.Config.Revision}, err
		}
		return out, state.ErrInvalidArgument
	}
	job := state.MCPCatalogObserverJob{AccountID: account, AppID: app, Role: role, Snapshot: view}
	clientSecret := ""
	if cfg.ClientSecretRef != "" {
		clientSecret, _, err = r.oauthSecret(ctx, job, cfg.ClientSecretRef)
		if err != nil {
			return out, err
		}
	}

	reservation, err := r.prepareExchangeRetirement(ctx, job, ids, clientSecret)
	if err != nil {
		return out, err
	}
	out.State = "authorization_uncertain"
	data, exchangeErr := r.exchangeOAuthCode(ctx, cfg, req.Code, envelope["VERIFIER"], envelope["REDIRECT_URI"], clientSecret)
	sealed, retirementErr := r.recordExchangeRetirement(ctx, account, reservation, ids, data)
	if retirementErr != nil {
		return out, retirementErr
	}
	if exchangeErr != nil {
		return out, errors.New("authorization unavailable")
	}
	scopes, err := compatibleOAuthScopes(cfg, cfg.RequestedScopes(), data.Scope)
	if err != nil {
		return out, state.ErrInvalidArgument
	}

	now := time.Now().UTC()
	expires := now.Add(time.Duration(data.ExpiresIn) * time.Second)
	oauth := api.MCPCatalogObserverOAuthStatus{RetirementID: reservation.ID, State: "connected", ExpiresAt: &expires, LastRenewalAt: &now, Ciphertext: sealed, SourceSHA256: digest([]byte("guided")), GrantedScopes: scopes}
	view, err = store.ChangeMCPCatalogOAuth(ctx, account, app, role, req.Revision, "finish", digest([]byte(req.State)), nil, &oauth)
	if err != nil {
		return out, err
	}
	return api.MCPCatalogOAuthResponse{Revision: view.Config.Revision, State: "connected"}, err
}
func oauthLoopbackRedirect(value string) bool {
	u, err := url.Parse(value)
	if err != nil {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	number, numberErr := strconv.Atoi(port)
	return err == nil && host == "127.0.0.1" && numberErr == nil && number > 0 && number <= 65535 && u.Scheme == "http" && u.User == nil && u.Path == "/callback" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawPath == "" && len(value) <= api.MCPCatalogObserverMaxEndpointBytes
}
func (r *ObserverRunner) exchangeOAuthCode(ctx context.Context, cfg *api.MCPCatalogObserverOAuthConfig, code, verifier, redirect, clientSecret string) (oauthRefreshResponse, error) {
	var data oauthRefreshResponse
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "client_id": {cfg.ClientID}}
	req, err := oauthTokenRequest(ctx, cfg, form, clientSecret)
	if err != nil {
		return data, err
	}
	if r.Client == nil {
		return data, errors.New("transport unavailable")
	}
	client := *r.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return data, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, api.MCPCatalogOAuthMaxResponseBytes+1))
	if err != nil || len(body) > api.MCPCatalogOAuthMaxResponseBytes || json.Unmarshal(body, &data) != nil || response.StatusCode != 200 || data.Error != "" || !strings.EqualFold(data.TokenType, "Bearer") || !oauthTokenValue(data.AccessToken) || !oauthTokenValue(data.RefreshToken) || data.ExpiresIn <= api.MCPCatalogOAuthRefreshSkewSeconds || data.ExpiresIn > api.MCPCatalogOAuthMaxExpirySeconds {
		return data, errors.New("invalid authorization response")
	}
	return data, nil
}

func grantedOAuthScopes(requested []string, value string) ([]string, error) {
	scopes := requested
	if value != "" {
		scopes = strings.Fields(value)
	}
	if len(scopes) > api.MCPCatalogOAuthMaxScopes {
		return nil, state.ErrInvalidArgument
	}
	for _, scope := range scopes {
		allowed := len(requested) == 0
		for _, want := range requested {
			if want == scope {
				allowed = true
			}
		}
		if !allowed || !api.ValidMCPOAuthScope(scope) {
			return nil, state.ErrInvalidArgument
		}
	}
	return scopes, nil
}

func respondOAuth(w http.ResponseWriter, out any, err error) {
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "OAuth recovery refused", "Reload the connection; resolve uncertain issuance or wait for the current operation before retrying"))
		return
	}
	respond(w, out, err)
}
