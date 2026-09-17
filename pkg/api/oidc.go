// OIDC exchange DTOs (issue #270 / ADR-101). The struct tags
// match the openapi.yaml shape verbatim — the spec_compliance_test
// gate verifies both ends. The App field on the request is optional
// (omit-empty) so the bind-payload's audit-attribution path is
// conditional.
package api

// OIDCExchangeRequest is the body of POST /v1/auth/oidc/exchange.
// Provider is the customer-side IdP id (github/gitlab/circleci/oidc)
// used for audit attribution only — the issuer is pinned in the
// JWT `iss` claim and is what the server actually verifies against.
// Token is the raw IdP-issued JWT. Audience is the aud claim the
// customer pinned in the action and must match the trust policy's
// audience array verbatim. App is optional; it pins the exchange
// to one customer app so the audit row carries the app slug.
type OIDCExchangeRequest struct {
	Provider string `json:"provider"`
	Token    string `json:"token"`
	Audience string `json:"aud"`
	App      string `json:"app,omitempty"`
}

// OIDCExchangeResponse is the body of POST /v1/auth/oidc/exchange
// on success. Bearer is the opaque fp_oidc_<48 hex> token to put
// in Authorization: Bearer … on the deploy routes. ExpiresIn is
// the seconds-until-expiry (300 today, mirrored in OIDCBearerTTL).
// TokenID is the opaque row id useful for log correlation.
type OIDCExchangeResponse struct {
	Bearer    string `json:"bearer"`
	ExpiresIn int    `json:"expires_in"`
	TokenID   string `json:"token_id"`
}

// OAuthTokenExchangeRequest is the RFC 8693 form-encoded profile accepted by
// the OIDC exchange endpoint. Gregale exchanges JWT subject tokens for the
// deploy access-token type and requires one audience to select the trust
// policy.
type OAuthTokenExchangeRequest struct {
	GrantType          string `json:"grant_type"`
	SubjectToken       string `json:"subject_token"`
	SubjectTokenType   string `json:"subject_token_type"`
	Audience           string `json:"audience"`
	RequestedTokenType string `json:"requested_token_type,omitempty"`
	Scope              string `json:"scope,omitempty"`
}

// OAuthTokenExchangeResponse is the RFC 8693 success shape for an opaque
// deploy bearer issued by the OIDC exchange endpoint.
type OAuthTokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	Scope           string `json:"scope"`
}

// OAuthTokenExchangeError is the OAuth token-endpoint error shape returned by
// the RFC 8693 form profile. The legacy JSON profile keeps Problem details.
type OAuthTokenExchangeError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}
