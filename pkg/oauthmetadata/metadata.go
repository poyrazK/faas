// Package oauthmetadata serves Gregale's RFC 8414 authorization-server
// metadata document.
//
// Gregale exposes a narrowly scoped token-exchange service rather than a
// general-purpose customer authorization server. The published metadata
// therefore advertises only the RFC 8693 grant and the capabilities that the
// public token endpoint actually supports.
package oauthmetadata

import (
	"encoding/json"
	"net/http"
)

const (
	// Path is the RFC 8414 well-known resource mounted by gatewayd-public.
	Path = "/.well-known/oauth-authorization-server"

	// Issuer is the canonical public API issuer. It is deliberately stable and
	// does not come from the request Host header, which would allow an attacker
	// to publish metadata for an untrusted origin.
	Issuer = "https://api.gregale.dev"

	tokenEndpoint = Issuer + "/v1/auth/oidc/exchange"
)

const (
	tokenExchangeGrant = "urn:ietf:params:oauth:grant-type:token-exchange"
	jwtSubjectToken    = "urn:ietf:params:oauth:token-type:jwt"
)

// Metadata is the RFC 8414 JSON representation for Gregale's token service.
// ResponseTypesSupported is intentionally empty because no authorization
// endpoint or authorization-code response type is exposed by this service.
type Metadata struct {
	Issuer                            string   `json:"issuer"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	ResponseTypesSupported            []string `json:"response_types_supported"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	SubjectTokenTypesSupported        []string `json:"subject_token_types_supported,omitempty"`
}

// Document returns a fresh metadata value so callers cannot mutate the
// package's canonical slices.
func Document() Metadata {
	return Metadata{
		Issuer:                            Issuer,
		TokenEndpoint:                     tokenEndpoint,
		ResponseTypesSupported:            []string{},
		GrantTypesSupported:               []string{tokenExchangeGrant},
		ScopesSupported:                   []string{"deploy:write"},
		TokenEndpointAuthMethodsSupported: []string{"none"},
		SubjectTokenTypesSupported:        []string{jwtSubjectToken},
	}
}

// Handler returns the anonymous GET/HEAD handler for the RFC 8414 document.
// Metadata is public and cacheable, but remains immutable at runtime.
func Handler() http.Handler {
	document, err := json.Marshal(Document())
	if err != nil {
		// Document contains only fixed, JSON-safe values. Keep the failure
		// explicit if that invariant ever changes.
		panic("oauthmetadata: marshal document: " + err.Error())
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(document)
		}
	})
}
