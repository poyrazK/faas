package mcpcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func oauthTokenRequest(ctx context.Context, cfg *api.MCPCatalogObserverOAuthConfig, form url.Values, secret string) (*http.Request, error) {
	method := cfg.ResolvedTokenAuthMethod()
	if method != "none" && method != "client_secret_basic" && method != "client_secret_post" {
		return nil, errors.New("unsupported token authentication")
	}
	if (method == "none") != (secret == "") {
		return nil, errors.New("token authentication credential unavailable")
	}
	if cfg.Resource != "" {
		form.Set("resource", cfg.Resource)
	}
	if method == "client_secret_post" {
		form.Set("client_secret", secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if method == "client_secret_basic" {
		req.SetBasicAuth(url.QueryEscape(cfg.ClientID), url.QueryEscape(secret))
	}
	return req, nil
}
func matchesOAuthCallbackPort(redirect string, port int) bool {
	u, err := url.Parse(redirect)
	return err == nil && (port == 0 || u.Port() == strconv.Itoa(port))
}
func compatibleOAuthScopes(cfg *api.MCPCatalogObserverOAuthConfig, requested []string, value string) ([]string, error) {
	if cfg.Profile != "google" {
		return grantedOAuthScopes(requested, value)
	}
	canonical := func(scope string) string {
		switch scope {
		case "email":
			return "https://www.googleapis.com/auth/userinfo.email"
		case "profile":
			return "https://www.googleapis.com/auth/userinfo.profile"
		}
		return scope
	}
	normalized := make([]string, len(requested))
	for i, scope := range requested {
		normalized[i] = canonical(scope)
	}
	if value != "" {
		scopes := strings.Fields(value)
		for i, scope := range scopes {
			scopes[i] = canonical(scope)
		}
		value = strings.Join(scopes, " ")
	}
	return grantedOAuthScopes(normalized, value)
}

type oauthProviderMetadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	PKCEMethods           []string `json:"code_challenge_methods_supported"`
	Scopes                []string `json:"scopes_supported"`
	AuthMethods           []string `json:"token_endpoint_auth_methods_supported"`
	Grants                []string `json:"grant_types_supported"`
	ResponseTypes         []string `json:"response_types_supported"`
}

func (r *ObserverRunner) checkOAuthCompatibility(ctx context.Context, cfg *api.MCPCatalogObserverOAuthConfig, revision string) api.MCPCatalogOAuthResponse {
	out := api.MCPCatalogOAuthResponse{Revision: revision, State: "compatible"}
	add := func(name, state, reason string) {
		out.Checks = append(out.Checks, api.MCPCatalogOAuthCheck{Name: name, State: state, Reason: reason})
		if state == "failed" {
			out.State = "unsupported"
		} else if state == "unverified" && out.State == "compatible" {
			out.State = "unverified"
		}
	}
	if cfg.DiscoveryURL == "" {
		add("discovery", "unverified", "metadata_not_configured")
		return out
	}
	if cfg.Validate() != nil || r.Client == nil {
		out.State = "unavailable"
		add("discovery", "failed", "metadata_unavailable")
		out.State = "unavailable"
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.DiscoveryURL, nil)
	if err != nil {
		out.State = "unavailable"
		return out
	}
	req.Header.Set("Accept", "application/json")
	client := *r.Client
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	var metadata oauthProviderMetadata
	valid := err == nil
	if valid {
		defer func() { _ = response.Body.Close() }()
		body, e := io.ReadAll(io.LimitReader(response.Body, api.MCPCatalogOAuthMaxResponseBytes+1))
		valid = e == nil && response.StatusCode == 200 && len(body) <= api.MCPCatalogOAuthMaxResponseBytes && json.Unmarshal(body, &metadata) == nil
	}
	if !valid {
		add("discovery", "failed", "metadata_unavailable")
		out.State = "unavailable"
		return out
	}
	exact := func(name string, ok bool) {
		if ok {
			add(name, "passed", "")
		} else {
			add(name, "failed", "reviewed_endpoint_mismatch")
		}
	}
	exact("issuer", metadata.Issuer == cfg.Issuer)
	exact("authorization_endpoint", metadata.AuthorizationEndpoint == cfg.AuthorizationEndpoint)
	exact("token_endpoint", metadata.TokenEndpoint == cfg.TokenEndpoint)
	declared := func(name string, values, wanted []string) {
		if len(values) == 0 {
			add(name, "unverified", "capability_not_advertised")
			return
		}
		for _, want := range wanted {
			found := false
			for _, value := range values {
				if value == want {
					found = true
				}
			}
			if !found {
				add(name, "failed", "required_capability_not_supported")
				return
			}
		}
		add(name, "passed", "")
	}
	declared("pkce", metadata.PKCEMethods, []string{"S256"})
	methods := metadata.AuthMethods
	if methods == nil {
		methods = []string{"client_secret_basic"}
	}
	declared("token_authentication", methods, []string{cfg.ResolvedTokenAuthMethod()})
	declared("grant_types", metadata.Grants, []string{"authorization_code", "refresh_token"})
	declared("response_types", metadata.ResponseTypes, []string{"code"})
	scopes := cfg.RequestedScopes()
	verified := true
	for _, want := range scopes {
		found := false
		for _, value := range metadata.Scopes {
			if value == want {
				found = true
			}
		}
		if !found {
			verified = false
		}
	}
	if verified {
		add("scopes", "passed", "")
	} else {
		add("scopes", "unverified", "scope_not_advertised")
	}
	return out
}
