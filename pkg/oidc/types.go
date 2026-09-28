// Package oidc is the customer-facing OIDC / keyless deploy auth path
// (issue #270 / ADR-101). It closes the ADR-093 §125 deferral: a CI
// runner sends an IdP-issued JWT to POST /v1/auth/oidc/exchange and
// receives a short-lived opaque bearer (5 min TTL). The bearer then
// flows through the existing Authorization: Bearer path on the deploy
// routes unchanged — pkg/oidc only owns the exchange + the lookup of
// exchanged tokens; the auth chain downstream reuses
// state.APIKey / pkg/auth/middleware unchanged.
//
// The package is a leaf: it imports pkg/edgejwks, pkg/state, pkg/api
// and nothing else. The cmd-side wiring lives in cmd/apid (the
// exchange handler) and pkg/state (the concrete AuthenticateOIDCBearer
// projection on Store).
//
// Trust policies are account-scoped, 1:N per (account_id, issuer_url).
// Auto-create on first exchange so customers don't have to touch the
// dashboard before their first CI deploy (ADR-101 §6). The default
// capability remains deploy:write; the environment-preflight profile is a
// separate, narrower read-and-qualify capability.
package oidc

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// OIDCBearerTTL is the lifetime of an exchanged bearer. Picked to
// comfortably cover a long CI deploy (the action default --wait-timeout
// is 600 s; a customer with retry + slow build still fits inside one
// TTL). Short enough that a leaked bearer expires before the next
// operator rotation window.
const OIDCBearerTTL = 5 * time.Minute

// OIDCTrustPolicy is a per-(account, issuer) admission rule for
// OIDC-derived bearer exchanges. The actual struct lives in pkg/state
// (so the store interface signatures resolve without an import cycle);
// this package re-exports it as a type alias so callers can write
// `oidc.OIDCTrustPolicy` without touching the lower-level package.
//
// Same shape as state.OIDCTrustPolicy — no method set on the package-
// local alias. ToAPIKey is on the token type, not the policy.
type OIDCTrustPolicy = state.OIDCTrustPolicy

// ExchangedToken is the persisted row behind a short-lived bearer.
// The wire-side bearer (fp_oidc_<48 hex>) is hashed; the row carries
// the OIDC provenance (issuer/sub/aud/jti) so the audit reader can
// answer "which CI job shipped this?" without joining against the
// IdP. TTL bounds GDPR deletion (the account_id FK CASCADE is the
// contractual guarantee; the 5-min TTL is the operational one).
//
// The struct lives in pkg/state for the same cycle-avoidance reason
// as OIDCTrustPolicy. The package-local method set (ToAPIKey) lives
// here so the handler can call it without exposing pkg/oidc internals
// to pkg/state.
type ExchangedToken = state.OIDCExchangedToken

// ToAPIKey projects an ExchangedToken into a synthetic state.APIKey
// for the principal stamp in pkg/auth/middleware. The method is
// inherited from the canonical state.OIDCExchangedToken (the alias
// type carries the method set over). See pkg/state/types.go.
//
// Status is fixed at "active" — there is no revoked
// state for 5-min TTL tokens; the row disappears via TTL or FK
// CASCADE, both of which surface as ErrNotFound to the lookup.

// CapabilityEnvironmentPreflight requests the narrow read-and-qualify
// authority required by environment preflight. It cannot deploy, promote,
// inspect secrets, or change environment configuration.
const CapabilityEnvironmentPreflight = "environment-preflight"

// OIDCBearerScopes is the default closed scope set for an OIDC-derived
// bearer. The historical default remains deploy-only.
func OIDCBearerScopes() []string {
	// Returns a fresh slice per call so callers can mutate freely
	// (mirrors state.APIKey.Scopes slice semantics).
	return []string{"deploy:write"}
}

// OIDCBearerScopesForCapability maps a closed capability name to fixed
// scopes. Clients cannot request arbitrary scopes through OIDC exchange.
func OIDCBearerScopesForCapability(capability string) ([]string, error) {
	switch capability {
	case "":
		return OIDCBearerScopes(), nil
	case CapabilityEnvironmentPreflight:
		return []string{"project_environments:read", "project_environments:qualify"}, nil
	default:
		return nil, errors.New("unsupported OIDC capability")
	}
}

// ExchangeRequest is the JSON body of POST /v1/auth/oidc/exchange.
//
// App is optional but recommended — it pins the exchange to one
// customer app so the audit row carries "which app did CI ship?"
// in addition to "which CI job shipped it". Empty App means the
// exchange still succeeds but the audit row has app=NULL; CI scripts
// that deploy the same repo to multiple apps pass App explicitly.
type ExchangeRequest struct {
	Provider   string `json:"provider"`      // 'github' | 'gitlab' | 'circleci' | 'oidc' (generic)
	Token      string `json:"token"`         // raw IdP-issued JWT
	Audience   string `json:"aud"`           // the aud claim the customer pinned in the action
	App        string `json:"app,omitempty"` // optional app slug for audit attribution
	Capability string `json:"capability,omitempty"`
}

// ExchangeResponse is what /v1/auth/oidc/exchange returns on success.
// Bearer is the fp_oidc_<48 hex> opaque token to put in
// Authorization: Bearer … on routes permitted by the returned scope
// profile. ExpiresIn is the seconds-until-expiry for the caller's convenience
// (matches AWS STS AssumeRoleWithWebIdentity shape).
type ExchangeResponse struct {
	Bearer    string   `json:"bearer"`
	ExpiresIn int      `json:"expires_in"`
	TokenID   string   `json:"token_id"` // opaque row id; useful for log correlation
	Scopes    []string `json:"scopes,omitempty"`
}

// time alias so the handler can use time helpers without re-importing
// the package. Cosmetic — `time.Now()` reads the same.
var _ = time.Now
