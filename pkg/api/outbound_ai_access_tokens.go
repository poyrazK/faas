package api

import (
	"net/http"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// OutboundAIAccessTokenPrefix starts every AI gateway access token
// (ADR-1431), so secret scanners can recognise one.
const OutboundAIAccessTokenPrefix = "ggai_"

var outboundAIAccessTokenName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// CreateOutboundAIAccessTokenRequest issues a token that lets a caller
// outside Gregale act as one app on the AI gateway (ADR-1431).
// IntegrationIDs narrows it to listed AI integrations; empty allows every
// AI integration bound to the app, and the account's routes.
type CreateOutboundAIAccessTokenRequest struct {
	Name           string   `json:"name"`
	ExpiresInHours *int     `json:"expires_in_hours,omitempty"`
	IntegrationIDs []string `json:"integration_ids,omitempty"`
}

// Normalized fills the default lifetime.
func (r CreateOutboundAIAccessTokenRequest) Normalized() CreateOutboundAIAccessTokenRequest {
	r.ExpiresInHours = intOrDefault(r.ExpiresInHours, OutboundAIAccessTokenTTLDefaultHours)
	if r.IntegrationIDs == nil {
		r.IntegrationIDs = []string{}
	}
	return r
}

// Valid checks the name, lifetime and integration list.
func (r CreateOutboundAIAccessTokenRequest) Valid() bool {
	r = r.Normalized()
	if !outboundAIAccessTokenName.MatchString(r.Name) || *r.ExpiresInHours < 1 || *r.ExpiresInHours > OutboundAIAccessTokenTTLMaxHours ||
		len(r.IntegrationIDs) > OutboundAIAccessTokenIntegrationsMax {
		return false
	}
	seen := make(map[string]struct{}, len(r.IntegrationIDs))
	for _, id := range r.IntegrationIDs {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
		if _, dup := seen[id]; dup {
			return false
		}
		seen[id] = struct{}{}
	}
	return true
}

// OutboundAIAccessToken describes an issued token; the secret is shown only
// once, when it is created.
type OutboundAIAccessToken struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	AppID          string     `json:"app_id"`
	Prefix         string     `json:"prefix"`
	IntegrationIDs []string   `json:"integration_ids"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	LastUsedAt     *time.Time `json:"last_used_at"`
	RevokedAt      *time.Time `json:"revoked_at"`
}

// OutboundAIAccessTokenList is an app's tokens, newest first.
type OutboundAIAccessTokenList struct {
	Items []OutboundAIAccessToken `json:"items"`
}

// CreatedOutboundAIAccessToken carries the secret of a new token. Callers
// put it where their SDK puts its API key.
type CreatedOutboundAIAccessToken struct {
	AccessToken OutboundAIAccessToken `json:"access_token"`
	Token       string                `json:"token"`
}

// ErrOutboundAIAccessTokenLimit is the problem for an app that holds its
// most live access tokens.
func ErrOutboundAIAccessTokenLimit(observed int64) *Problem {
	return NewProblem(http.StatusConflict, "outbound_ai_access_token_limit", "AI access token limit reached",
		"An app can hold at most 20 live AI gateway access tokens; revoke one first.").
		WithLimit(OutboundAIAccessTokensPerApp, observed).
		WithDocs(docsBase + "/cli#outbound")
}
