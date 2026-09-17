package oidc

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
)

// RFC 8693 identifiers used by the form-encoded token exchange profile.
const (
	TokenExchangeGrantType       = "urn:ietf:params:oauth:grant-type:token-exchange"
	TokenExchangeJWTTokenType    = "urn:ietf:params:oauth:token-type:jwt"
	TokenExchangeAccessTokenType = "urn:ietf:params:oauth:token-type:access_token"
)

// TokenExchangeResponse is the RFC 8693 success shape. Gregale issues an
// opaque bearer as an access token and never issues refresh tokens from an
// OIDC assertion exchange.
type TokenExchangeResponse struct {
	AccessToken     string `json:"access_token"`
	IssuedTokenType string `json:"issued_token_type"`
	TokenType       string `json:"token_type"`
	ExpiresIn       int    `json:"expires_in"`
	Scope           string `json:"scope"`
}

// TokenExchangeError is the RFC 8693 / OAuth 2.0 error shape used for the
// form-encoded profile. The legacy JSON profile continues to return the
// platform's application/problem+json envelope.
type TokenExchangeError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

type rfc8693InputError struct {
	code        string
	description string
}

func (e *rfc8693InputError) Error() string { return e.description }

func newRFC8693InputError(code, description string) error {
	return &rfc8693InputError{code: code, description: description}
}

func rfc8693MediaType(r *http.Request) bool {
	if r == nil {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return err == nil && mediaType == "application/x-www-form-urlencoded"
}

// parseRFC8693Request validates the narrow Gregale profile of RFC 8693. The
// endpoint exchanges a JWT subject token for a deploy bearer, so one audience
// is required to select the account trust policy and only deploy:write scope
// can be issued. Resource and actor tokens are deliberately rejected rather
// than silently ignored.
func parseRFC8693Request(w http.ResponseWriter, r *http.Request) (ExchangeRequest, error) {
	if r == nil || r.Body == nil {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "request body is required")
	}
	if r.ContentLength > maxExchangeBodyBytes {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "request body is too large")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxExchangeBodyBytes)
	if err := r.ParseForm(); err != nil {
		if errors.Is(err, io.EOF) {
			return ExchangeRequest{}, newRFC8693InputError("invalid_request", "request body is required")
		}
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", err.Error())
	}
	form := r.PostForm
	if got := form.Get("grant_type"); got != TokenExchangeGrantType {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "grant_type must be the RFC 8693 token-exchange grant")
	}
	if got := form.Get("subject_token_type"); got != TokenExchangeJWTTokenType {
		return ExchangeRequest{}, newRFC8693InputError("unsupported_subject_token_type", "subject_token_type must be urn:ietf:params:oauth:token-type:jwt")
	}
	subjectToken := strings.TrimSpace(form.Get("subject_token"))
	if subjectToken == "" {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "subject_token is required")
	}
	if len(form["subject_token"]) != 1 || len(form["grant_type"]) != 1 || len(form["subject_token_type"]) != 1 {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "grant_type, subject_token, and subject_token_type must each occur once")
	}
	if len(form["audience"]) != 1 || strings.TrimSpace(form.Get("audience")) == "" {
		return ExchangeRequest{}, newRFC8693InputError("invalid_target", "exactly one audience is required by the Gregale token-exchange profile")
	}
	for _, resource := range form["resource"] {
		if strings.TrimSpace(resource) != "" {
			return ExchangeRequest{}, newRFC8693InputError("invalid_target", "resource is not supported; use audience")
		}
	}
	if len(form["actor_token"]) > 0 || len(form["actor_token_type"]) > 0 {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "actor_token delegation is not supported")
	}
	for _, name := range []string{"client_id", "client_secret", "client_assertion", "client_assertion_type"} {
		if len(form[name]) > 0 {
			return ExchangeRequest{}, newRFC8693InputError("invalid_request", "client authentication is not supported on this endpoint")
		}
	}
	if len(form["requested_token_type"]) > 1 {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "requested_token_type must occur at most once")
	}
	if requested := strings.TrimSpace(form.Get("requested_token_type")); requested != "" && requested != TokenExchangeAccessTokenType {
		return ExchangeRequest{}, newRFC8693InputError("unsupported_token_type", "requested_token_type must be an access token")
	}
	if len(form["scope"]) > 1 {
		return ExchangeRequest{}, newRFC8693InputError("invalid_request", "scope must occur at most once")
	}
	if scope := strings.TrimSpace(form.Get("scope")); scope != "" && scope != "deploy:write" {
		return ExchangeRequest{}, newRFC8693InputError("invalid_scope", "only deploy:write can be issued")
	}
	return ExchangeRequest{
		Provider: "oidc",
		Token:    subjectToken,
		Audience: strings.TrimSpace(form.Get("audience")),
	}, nil
}

func writeRFC8693Error(w http.ResponseWriter, status int, code, description string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = writeJSONValue(w, TokenExchangeError{Error: code, ErrorDescription: description})
}

func writeJSONValue(w http.ResponseWriter, body any) error {
	return json.NewEncoder(w).Encode(body)
}

func tokenExchangeErrorFromStatus(status int) string {
	if status >= 500 || status == http.StatusTooManyRequests {
		return "temporarily_unavailable"
	}
	if status == http.StatusUnauthorized {
		return "invalid_grant"
	}
	return "invalid_request"
}

func tokenExchangeDescription(body []byte) string {
	var p struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &p); err == nil && strings.TrimSpace(p.Detail) != "" {
		return p.Detail
	}
	return "token exchange request was rejected"
}
